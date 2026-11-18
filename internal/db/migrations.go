package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationsEmbed embed.FS

func migrationsFS() (map[string][]byte, error) {
	out := map[string][]byte{}
	return out, fs.WalkDir(migrationsEmbed, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := migrationsEmbed.ReadFile(path)
		if err != nil {
			return err
		}
		name := path[len("migrations/"):]
		out[name] = b
		return nil
	})
}
