// Command dbreset drops a test database (its name starts with academy_e2e or academy_test) on the test server
// (ACADEMY_TEST_DATABASE_URL, default `make db`'s container), so the next `academy serve` creates it afresh: the e2e
// suite's (`make e2e`). It refuses any other name.
//
//	dbreset academy_e2e
package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dbreset:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 || (!strings.HasPrefix(args[0], "academy_e2e") && !strings.HasPrefix(args[0], "academy_test")) {
		return fmt.Errorf("usage: dbreset academy_e2e|academy_test... (test databases only)")
	}
	server := os.Getenv("ACADEMY_TEST_DATABASE_URL")
	if server == "" {
		server = postgres.DefaultURL
	}
	u, err := url.Parse(server)
	if err != nil {
		return err
	}
	u.Path = "/postgres"
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	drop := "DROP DATABASE IF EXISTS " + postgres.QuoteIdent(args[0]) + " WITH (FORCE)" //nolint:gosec // quoted
	_, err = db.ExecContext(context.Background(), drop)                                 //nolint:gosec // a quoted test database name
	return err
}
