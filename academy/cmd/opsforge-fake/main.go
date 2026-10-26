// Command opsforge-fake serves the fake OpsForge API (internal/opsforgeapi/opsforgefake) for the e2e suite: an
// empty lab, every step valid, workspaces in memory, an echoing terminal. It writes its token to -token-file and,
// with -url-file, its URL once it listens (-addr 127.0.0.1:0 picks a free port).
//
//	opsforge-fake -addr 127.0.0.1:18092 -token-file /tmp/token [-url-file /tmp/url]
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/opsforgeapi/opsforgefake"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18092", "listen address")
	tokenFile := flag.String("token-file", "", "where to write the token (required)")
	urlFile := flag.String("url-file", "", "where to write the URL once listening (optional)")
	flag.Parse()
	if *tokenFile == "" {
		fmt.Fprintln(os.Stderr, "opsforge-fake: -token-file is required")
		os.Exit(2)
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	if err := os.WriteFile(*tokenFile, []byte(token+"\n"), 0o600); err != nil {
		log.Fatal(err)
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	if *urlFile != "" {
		if err := os.WriteFile(*urlFile, []byte("http://"+ln.Addr().String()+"\n"), 0o600); err != nil {
			log.Fatal(err)
		}
	}
	srv := &http.Server{Handler: opsforgefake.New(token), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.Serve(ln))
}
