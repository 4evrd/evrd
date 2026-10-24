package store

// Secrets at rest. Network passwords (SASL/NickServ, per-server PASS), the
// services login command and module settings (perform's commands can hold
// an OPER or IDENTIFY password) are encrypted in the database with AES-256-GCM under a server key that lives
// outside it (config secret_key / EVRD_SECRET_KEY), so a dump or backup of
// the database doesn't hand them out. Every node needs the same key.
//
// Stored form: "enc:v1:<key id>:<base64(nonce|ciphertext)>". The key id is
// a short hash of the key, so old keys (secret_key_old) can still decrypt
// while EncryptSecrets rewrites everything under the current one. Values
// without the prefix are plaintext from before a key was set.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"gorm.io/gorm/schema"
)

const secretPrefix = "enc:v1:"

// ErrSecretKey means a stored secret can't be decrypted with the keys given.
var ErrSecretKey = errors.New("store: can't decrypt a saved secret — is secret_key set to the key it was saved with?")

type keyring struct {
	mu      sync.RWMutex
	current string // key id; "" = no key: secrets are stored as plaintext
	aeads   map[string]cipher.AEAD
}

var keys = &keyring{aeads: map[string]cipher.AEAD{}}

func init() {
	schema.RegisterSerializer("secret", secretSerializer{})
	schema.RegisterSerializer("secretservers", serversSerializer{})
	schema.RegisterSerializer("secretjson", secretJSONSerializer{})
}

// SetSecretKeys sets the key secrets are encrypted with (32 bytes) and any
// older keys still accepted for decrypting. A nil current key stores
// secrets as plaintext. Call it before Open.
func SetSecretKeys(current []byte, old ...[]byte) error {
	aeads := map[string]cipher.AEAD{}
	add := func(k []byte) (string, error) {
		if len(k) != 32 {
			return "", fmt.Errorf("store: a secret key is 32 bytes, got %d", len(k))
		}
		block, err := aes.NewCipher(k)
		if err != nil {
			return "", err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return "", err
		}
		id := keyID(k)
		aeads[id] = gcm
		return id, nil
	}
	cur := ""
	if current != nil {
		id, err := add(current)
		if err != nil {
			return err
		}
		cur = id
	}
	for _, k := range old {
		if _, err := add(k); err != nil {
			return err
		}
	}
	keys.mu.Lock()
	keys.current, keys.aeads = cur, aeads
	keys.mu.Unlock()
	return nil
}

// SecretsEncrypted reports whether a key is set.
func SecretsEncrypted() bool {
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	return keys.current != ""
}

func keyID(k []byte) string {
	sum := sha256.Sum256(append([]byte("evrd-secret-key:"), k...))
	return hex.EncodeToString(sum[:4])
}

// sealSecret encrypts s under the current key ("" stays "").
func sealSecret(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	keys.mu.RLock()
	id, gcm := keys.current, keys.aeads[keys.current]
	keys.mu.RUnlock()
	if id == "" {
		return s, nil
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(s), []byte(id))
	return secretPrefix + id + ":" + base64.RawStdEncoding.EncodeToString(ct), nil
}

// openSecret decrypts a stored value; plaintext passes through.
func openSecret(stored string) (string, error) {
	rest, ok := strings.CutPrefix(stored, secretPrefix)
	if !ok {
		return stored, nil
	}
	id, b64, ok := strings.Cut(rest, ":")
	if !ok {
		return "", ErrSecretKey
	}
	keys.mu.RLock()
	gcm := keys.aeads[id]
	keys.mu.RUnlock()
	if gcm == nil {
		return "", ErrSecretKey
	}
	ct, err := base64.RawStdEncoding.DecodeString(b64)
	if err != nil || len(ct) < gcm.NonceSize() {
		return "", ErrSecretKey
	}
	plain, err := gcm.Open(nil, ct[:gcm.NonceSize()], ct[gcm.NonceSize():], []byte(id))
	if err != nil {
		return "", ErrSecretKey
	}
	return string(plain), nil
}

// current reports whether a stored value is already sealed under the
// current key (or is empty / there's no key and it's plaintext).
func current(stored string) bool {
	keys.mu.RLock()
	id := keys.current
	keys.mu.RUnlock()
	if stored == "" {
		return true
	}
	if id == "" {
		return !strings.HasPrefix(stored, secretPrefix)
	}
	return strings.HasPrefix(stored, secretPrefix+id+":")
}

// secretSerializer stores a string field encrypted.
type secretSerializer struct{}

func (secretSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue any) error {
	var s string
	switch v := dbValue.(type) {
	case nil:
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("store: unexpected %T for a secret", dbValue)
	}
	plain, err := openSecret(s)
	if err != nil {
		return err
	}
	return field.Set(ctx, dst, plain)
}

func (secretSerializer) Value(_ context.Context, _ *schema.Field, _ reflect.Value, fieldValue any) (any, error) {
	s, _ := fieldValue.(string)
	return sealSecret(s)
}

// serversSerializer stores a network's server list as JSON with each
// server's password encrypted.
type serversSerializer struct{}

func (serversSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue any) error {
	var raw []byte
	switch v := dbValue.(type) {
	case nil:
		return field.Set(ctx, dst, []Server(nil))
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		return fmt.Errorf("store: unexpected %T for servers", dbValue)
	}
	var list []Server
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &list); err != nil {
			return err
		}
	}
	for i := range list {
		p, err := openSecret(list[i].Password)
		if err != nil {
			return err
		}
		list[i].Password = p
	}
	return field.Set(ctx, dst, list)
}

func (serversSerializer) Value(_ context.Context, _ *schema.Field, _ reflect.Value, fieldValue any) (any, error) {
	list, _ := fieldValue.([]Server)
	out := make([]Server, len(list))
	for i, s := range list {
		p, err := sealSecret(s.Password)
		if err != nil {
			return nil, err
		}
		s.Password = p
		out[i] = s
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// secretJSONSerializer stores a field as JSON, encrypted as a whole. JSON
// saved before it was encrypted (the plain json serializer) still reads.
type secretJSONSerializer struct{}

func (secretJSONSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue any) error {
	var stored string
	switch v := dbValue.(type) {
	case nil:
	case string:
		stored = v
	case []byte:
		stored = string(v)
	default:
		return fmt.Errorf("store: unexpected %T for a secret", dbValue)
	}
	plain, err := openSecret(stored)
	if err != nil {
		return err
	}
	out := reflect.New(field.FieldType)
	if plain != "" {
		if err := json.Unmarshal([]byte(plain), out.Interface()); err != nil {
			return err
		}
	}
	return field.Set(ctx, dst, out.Elem().Interface())
}

func (secretJSONSerializer) Value(_ context.Context, _ *schema.Field, _ reflect.Value, fieldValue any) (any, error) {
	b, err := json.Marshal(fieldValue)
	if err != nil {
		return nil, err
	}
	if string(b) == "null" || string(b) == "{}" {
		return "", nil // nothing to hide (and nothing for EncryptSecrets to redo)
	}
	return sealSecret(string(b))
}

// EncryptSecrets rewrites any network secret, saved setting or certificate
// that isn't
// encrypted under the current key (plaintext from before a key was set, or an
// old key's). It reports how many rows it rewrote.
func (s *gormStore) EncryptSecrets(ctx context.Context) (int, error) {
	db := s.db.DB.WithContext(ctx)
	var rows []struct {
		ID          int64
		Password    string
		Servers     string
		AuthCommand string
		Modules     string
	}
	if err := db.Table("networks").Select("id", "password", "coalesce(servers, '') as servers",
		"coalesce(auth_command, '') as auth_command", "coalesce(modules, '') as modules").Scan(&rows).Error; err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		stale := !current(r.Password) || !current(r.AuthCommand) || !current(r.Modules)
		var list []Server
		if r.Servers != "" && json.Unmarshal([]byte(r.Servers), &list) == nil {
			for _, sv := range list {
				stale = stale || !current(sv.Password)
			}
		}
		if !stale {
			continue
		}
		var net Network
		if err := db.First(&net, r.ID).Error; err != nil {
			return n, err
		}
		if err := db.Model(&Network{ID: net.ID}).Select("password", "servers", "auth_command", "modules").Updates(&net).Error; err != nil {
			return n, err
		}
		n++
	}

	// Settings saved in the admin UI are sealed too (client secrets live
	// there): rewrite those the same way.
	var srows []struct {
		Key   string
		Value string
	}
	if err := db.Table("settings").Select("key", "value").Scan(&srows).Error; err != nil {
		return n, err
	}
	for _, r := range srows {
		if current(r.Value) {
			continue
		}
		var st Setting
		if err := db.First(&st, "key = ?", r.Key).Error; err != nil {
			return n, err
		}
		if err := db.Save(&st).Error; err != nil {
			return n, err
		}
		n++
	}

	// And the TLS certificates' keys.
	var crows []struct {
		Key  string
		Data string
	}
	if err := db.Table("cert_cache_entries").Select("key", "data").Scan(&crows).Error; err != nil {
		return n, err
	}
	for _, r := range crows {
		if current(r.Data) {
			continue
		}
		var e CertCacheEntry
		if err := db.First(&e, "key = ?", r.Key).Error; err != nil {
			return n, err
		}
		if err := db.Save(&e).Error; err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// SealSecret encrypts s under the current key, for writing it somewhere
// other than the database (a networks export). ok is false when no key is
// set: there's nothing to seal it with, and it mustn't go out as plaintext.
func SealSecret(s string) (sealed string, ok bool, err error) {
	if !SecretsEncrypted() {
		return "", false, nil
	}
	sealed, err = sealSecret(s)
	return sealed, err == nil, err
}

// OpenSecret decrypts a value SealSecret wrote (ErrSecretKey when it was
// sealed under a key this server doesn't have); plaintext passes through.
func OpenSecret(stored string) (string, error) { return openSecret(stored) }

// IsSealed reports whether a value is in SealSecret's form.
func IsSealed(s string) bool { return strings.HasPrefix(s, secretPrefix) }
