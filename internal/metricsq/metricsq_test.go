package metricsq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestQueriesSendURLCredentialsAsBasicAuth(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		got = user + ":" + pass
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"7"]}]}}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	u.User = url.UserPassword("evrd", "s3cret")
	v, err := New(u.String()).Scalar(context.Background(), "up")
	if err != nil || v != 7 || got != "evrd:s3cret" {
		t.Fatalf("v=%v err=%v auth=%q", v, err, got)
	}
}
