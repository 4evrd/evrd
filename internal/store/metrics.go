package store

import (
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"gorm.io/gorm"

	"github.com/4evrd/evrd/internal/telemetry"
)

// metricsPlugin records every query's duration (telemetry.DBDuration), by
// table, operation and result, in the context the repository was given.
type metricsPlugin struct{}

func (metricsPlugin) Name() string { return "evrd:metrics" }

const startedKey = "evrd:started"

func (metricsPlugin) Initialize(db *gorm.DB) error {
	cb := db.Callback()
	return errors.Join(
		cb.Create().Before("gorm:create").Register("evrd:start", startTimer),
		cb.Create().After("gorm:create").Register("evrd:record", record("create")),
		cb.Query().Before("gorm:query").Register("evrd:start", startTimer),
		cb.Query().After("gorm:query").Register("evrd:record", record("query")),
		cb.Update().Before("gorm:update").Register("evrd:start", startTimer),
		cb.Update().After("gorm:update").Register("evrd:record", record("update")),
		cb.Delete().Before("gorm:delete").Register("evrd:start", startTimer),
		cb.Delete().After("gorm:delete").Register("evrd:record", record("delete")),
		cb.Row().Before("gorm:row").Register("evrd:start", startTimer),
		cb.Row().After("gorm:row").Register("evrd:record", record("row")),
		cb.Raw().Before("gorm:raw").Register("evrd:start", startTimer),
		cb.Raw().After("gorm:raw").Register("evrd:record", record("raw")),
	)
}

func startTimer(db *gorm.DB) { db.InstanceSet(startedKey, time.Now()) }

func record(op string) func(*gorm.DB) {
	return func(db *gorm.DB) {
		v, ok := db.InstanceGet(startedKey)
		if !ok {
			return
		}
		result := "ok"
		if db.Error != nil && !errors.Is(db.Error, gorm.ErrRecordNotFound) {
			result = "error"
		}
		telemetry.DBDuration.Record(db.Statement.Context, time.Since(v.(time.Time)).Seconds(),
			metric.WithAttributes(attribute.String("table", db.Statement.Table), attribute.String("op", op), attribute.String("result", result)))
	}
}
