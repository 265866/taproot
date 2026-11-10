package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// A panic inside the transaction must not leave the write lock held on a
// pooled connection, which would stall every other writer.
func TestWithImmediateTxReleasesWriteLockOnPanic(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Config{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE txtest (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	errBoom := errors.New("boom")
	func() {
		defer func() {
			if p := recover(); p != errBoom {
				t.Fatalf("recovered %v, want the original panic", p)
			}
		}()
		_ = WithImmediateTx(ctx, db.DB, func(c *sql.Conn) error {
			if _, err := c.ExecContext(ctx, `INSERT INTO txtest VALUES (1)`); err != nil {
				return err
			}
			panic(errBoom)
		})
	}()

	// Hold one connection so the write below may draw another one.
	held, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, conn := range []interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}{held, db} {
		if _, err := conn.ExecContext(short, `INSERT INTO txtest VALUES (2)`); err != nil {
			t.Fatalf("write after a panicked transaction: %v", err)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM txtest WHERE v = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the panicked transaction's insert was kept: %d rows", n)
	}
}
