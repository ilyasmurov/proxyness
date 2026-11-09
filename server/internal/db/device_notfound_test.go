package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

// A database with a devices table that is always empty (or unreachable), so
// the not-found / DB-error split of getDeviceByKeyDB runs without Postgres.
type fakeConnector struct{ connectErr error }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) {
	if c.connectErr != nil {
		return nil, c.connectErr
	}
	return fakeConn{}, nil
}
func (c fakeConnector) Driver() driver.Driver { return fakeDriver{c} }

type fakeDriver struct{ c fakeConnector }

func (d fakeDriver) Open(string) (driver.Conn, error) { return d.c.Connect(context.Background()) }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return fakeStmt{}, nil }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("no tx") }

type fakeStmt struct{}

func (fakeStmt) Close() error                               { return nil }
func (fakeStmt) NumInput() int                              { return -1 }
func (fakeStmt) Exec([]driver.Value) (driver.Result, error) { return nil, errors.New("no exec") }
func (fakeStmt) Query([]driver.Value) (driver.Rows, error)  { return fakeRows{}, nil }

type fakeRows struct{}

func (fakeRows) Columns() []string {
	return []string{"id", "user_id", "uname", "name", "key", "active", "client_version", "created_at"}
}
func (fakeRows) Close() error              { return nil }
func (fakeRows) Next([]driver.Value) error { return io.EOF }

// The proxy re-checks a device on a live mux session and must tell a deleted
// device (cut the session) from a database hiccup (keep it): the first is
// ErrDeviceNotFound, anything else is not.
func TestGetDeviceByKeySeparatesNotFoundFromDBErrors(t *testing.T) {
	const key = "abcd"

	missing := &DB{sql: sql.OpenDB(fakeConnector{}), cache: newDeviceCache()}
	defer missing.sql.Close()
	_, err := missing.GetDeviceByKey(key)
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("empty table: err = %v, want ErrDeviceNotFound", err)
	}
	// The message callers log and match on stays as it was.
	if want := `device not found for key "abcd"`; err == nil || err.Error() != want {
		t.Errorf("err text = %q, want %q", err, want)
	}
	// The cache hands the same error back for the next minute.
	if _, err := missing.GetDeviceByKey(key); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("cached not-found: err = %v, want ErrDeviceNotFound", err)
	}

	down := &DB{sql: sql.OpenDB(fakeConnector{connectErr: errors.New("connection refused")}), cache: newDeviceCache()}
	defer down.sql.Close()
	_, err = down.GetDeviceByKey(key)
	if err == nil || errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("unreachable DB: err = %v, want an error that is not ErrDeviceNotFound", err)
	}
	if !strings.Contains(err.Error(), "get device by key") {
		t.Errorf("DB error lost its wording: %v", err)
	}
}
