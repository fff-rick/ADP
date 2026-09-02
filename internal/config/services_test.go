package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServiceCatalogAndResolveByType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.cnf")
	contents := "[mysql_prod]\ntype=mysql\nhost=mysql.internal\nport=3306\nuser=backup\npassword=secret\n\n[redis_prod]\ntype=redis\nhost=redis.internal\nport=6379\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadServiceCatalog(path)
	if err != nil {
		t.Fatalf("LoadServiceCatalog() error = %v", err)
	}
	profile, err := catalog.Resolve("mysql_prod", "mysql")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if profile.Host != "mysql.internal" || profile.Password != "secret" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if _, err := catalog.Resolve("mysql_prod", "redis"); err == nil {
		t.Fatal("expected type mismatch")
	}
}

func TestServiceCatalogResolveAutoRequiresOneProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.cnf")
	contents := "[redis_one]\ntype=redis\nhost=redis-1.internal\n\n[mysql_one]\ntype=mysql\nhost=mysql.internal\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadServiceCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := catalog.ResolveAuto("redis")
	if err != nil || profile.Name != "redis_one" {
		t.Fatalf("ResolveAuto(redis) = %#v, %v", profile, err)
	}
	if _, err := catalog.ResolveAuto("nginx"); err == nil {
		t.Fatal("expected no-profile auto selection to fail")
	}
	contents += "\n[redis_two]\ntype=redis\nhost=redis-2.internal\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog, err = LoadServiceCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.ResolveAuto("redis"); err == nil {
		t.Fatal("expected ambiguous auto selection to fail")
	}
}

func TestLoadServiceCatalogRejectsInsecurePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "services.cnf")
	if err := os.WriteFile(path, []byte("[redis_prod]\ntype=redis\nhost=localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadServiceCatalog(path); err == nil {
		t.Fatal("expected insecure permissions error")
	}
}
