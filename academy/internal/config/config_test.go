package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/config"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestDefaultsDeriveFromTheDataFolder(t *testing.T) {
	cfg, err := config.Load(nil, env(map[string]string{"HOME": "/home/u"}))
	if err != nil {
		t.Fatal(err)
	}
	data := "/home/u/.local/share/opsforge-academy"
	if cfg.Data != data || cfg.Database != postgres.DefaultURL || cfg.OpsForgeTokenFile != data+"/opsforge-token" ||
		cfg.OpsForgeURL != "http://127.0.0.1:8091" || cfg.LegacyDB != "" || !strings.HasSuffix(cfg.Content, "/labs") ||
		cfg.Addr != "127.0.0.1:8090" || cfg.OpsForgeTimeout != 30*time.Second || cfg.LabTimeout != 20*time.Minute ||
		cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("got %+v", cfg)
	}
	cfg, err = config.Load(nil, env(map[string]string{"HOME": "/home/u", "XDG_DATA_HOME": "/xdg"}))
	if err != nil || cfg.Data != "/xdg/opsforge-academy" {
		t.Fatalf("XDG_DATA_HOME: %+v %v", cfg, err)
	}
}

func TestFlagsWinOverEnvironment(t *testing.T) {
	cfg, err := config.Load(
		[]string{"--addr", "localhost:9000", "--database", "postgres://u@db.example/academy", "--log-level", "debug", "--opsforge-url",
			"https://lab.example:8091"},
		env(map[string]string{"ACADEMY_DATA": "/d", "ACADEMY_ADDR": "127.0.0.1:1", "ACADEMY_LOG_LEVEL": "warn",
			"ACADEMY_DATABASE_URL":        "postgres://other/academy",
			"ACADEMY_OPSFORGE_TOKEN_FILE": "/secrets/token", "ACADEMY_OPSFORGE_CA_FILE": "/secrets/lab.pem"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "localhost:9000" || cfg.Database != "postgres://u@db.example/academy" || cfg.LogLevel != slog.LevelDebug ||
		cfg.OpsForgeURL != "https://lab.example:8091" || cfg.OpsForgeTokenFile != "/secrets/token" ||
		cfg.OpsForgeCAFile != "/secrets/lab.pem" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestInvalidConfigurationIsRejected(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"remote address":      {[]string{"--addr", "0.0.0.0:8090"}, "loopback"},
		"no port":             {[]string{"--addr", "127.0.0.1"}, "addr"},
		"unknown level":       {[]string{"--log-level", "loud"}, "log-level"},
		"zero timeout":        {[]string{"--opsforge-timeout", "0s"}, "opsforge-timeout"},
		"zero lab timeout":    {[]string{"--lab-timeout", "0s"}, "lab-timeout"},
		"unknown flag":        {[]string{"--nope"}, "nope"},
		"positional args":     {[]string{"extra"}, "unexpected"},
		"not a url":           {[]string{"--opsforge-url", "lab:8091"}, "opsforge-url"},
		"token in clear":      {[]string{"--opsforge-url", "http://10.0.0.5:8091"}, "https"},
		"ca without https":    {[]string{"--opsforge-ca-file", "/lab.pem"}, "opsforge-ca-file"},
		"not postgres":        {[]string{"--database", "sqlite:///academy.db"}, "database"},
		"no database name":    {[]string{"--database", "postgres://127.0.0.1:5433"}, "database"},
		"removed db":          {[]string{"--db", "/tmp/a.db"}, "db"},
		"removed root":        {[]string{"--root", "."}, "root"},
		"removed toolchain":   {[]string{"--toolchain", "./opsforge"}, "toolchain"},
		"removed workspaces":  {[]string{"--workspaces", "/w"}, "workspaces"},
		"zero shutdown grace": {[]string{"--shutdown-timeout", "0s"}, "shutdown-timeout"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := config.Load(tc.args, env(map[string]string{"HOME": "/home/u"}))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestCompileTakesPacksAndJSON(t *testing.T) {
	cfg, c, err := config.LoadCompile([]string{"--json", "--content", "/packs", "a", "b"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.JSON || strings.Join(c.Packs, " ") != "a b" || cfg.Content != "/packs" {
		t.Fatalf("got %+v %+v", c, cfg)
	}
	if _, err := config.Load([]string{"--json"}, env(nil)); err == nil {
		t.Fatal("serve takes no --json")
	}
}
