package infra

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DataAccessRef is one row for shared_databases. It captures three kinds
// of signals:
//
//   - Schema / Entity ownership: @Schema({ collection: 'X' }) or
//     @Entity({ name: 'X' }) → collection-level write access.
//   - Change streams: collection.watch(...) → change_stream access.
//   - Connection strings: MongooseModule.forRoot(process.env.X) → a
//     connection row whose ConnectionID is the env var name.
type DataAccessRef struct {
	RepoName     string
	ConnectionID string // env var name for connections, empty otherwise
	DBType       string // "mongodb" | "postgres" | "mysql" | "cloudsql"
	AccessType   string // "write" | "read" | "change_stream" | "connection"
	Collection   string // collection/table name (empty for connection rows)
	SourceFile   string
}

var (
	// Schema decorator with explicit collection: @Schema({ collection: 'X' })
	reMongooseSchema = regexp.MustCompile(`@Schema\([^)]*collection\s*:\s*['"]([^'"]+)['"]`)

	// TypeORM @Entity({ name: 'X' })
	reTypeOrmEntity = regexp.MustCompile(`@Entity\([^)]*name\s*:\s*['"]([^'"]+)['"]`)

	// collection.watch(...) or <ident>.watch(...)
	reChangeStream = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*watch\(`)

	// MongooseModule.forRoot(process.env.X) — the literal env var name.
	reMongoConnEnv = regexp.MustCompile(`MongooseModule\.(?:forRoot|forRootAsync)\([^)]*process\.env\.([A-Z_][A-Z0-9_]*)`)
	// Inside async factories: uri: process.env.X
	reUriEnv = regexp.MustCompile(`uri\s*:\s*process\.env\.([A-Z_][A-Z0-9_]*)`)
)

// ExtractDataAccess walks the repo and aggregates schema/entity/watch/
// connection signals across all TypeScript source files.
func ExtractDataAccess(root string) ([]DataAccessRef, error) {
	var out []DataAccessRef

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if skippedDirs[name] || (strings.HasPrefix(name, ".") && name != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if !strings.HasSuffix(name, ".ts") ||
			strings.HasSuffix(name, ".d.ts") ||
			strings.HasSuffix(name, ".spec.ts") ||
			strings.HasSuffix(name, ".test.ts") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		source := string(data)

		// Quick bail-out: file must contain at least one of the patterns.
		if !strings.Contains(source, "@Schema") &&
			!strings.Contains(source, "@Entity") &&
			!strings.Contains(source, ".watch(") &&
			!strings.Contains(source, "MongooseModule") &&
			!strings.Contains(source, "process.env") {
			return nil
		}

		rel, _ := filepath.Rel(root, path)

		// Mongoose schemas
		for _, m := range reMongooseSchema.FindAllStringSubmatch(source, -1) {
			out = append(out, DataAccessRef{
				DBType:     "mongodb",
				AccessType: "write",
				Collection: m[1],
				SourceFile: rel,
			})
		}

		// TypeORM entities
		for _, m := range reTypeOrmEntity.FindAllStringSubmatch(source, -1) {
			out = append(out, DataAccessRef{
				DBType:     "postgres",
				AccessType: "write",
				Collection: m[1],
				SourceFile: rel,
			})
		}

		// Change streams — only flag if the receiver looks like a mongo
		// collection/connection. Filter out obvious non-mongo .watch()
		// calls (RxJS observables, DOM watchers, etc.) by requiring the
		// source to also import from mongoose or nestjs/mongoose.
		isMongoCtx := strings.Contains(source, "@nestjs/mongoose") ||
			strings.Contains(source, "mongoose") ||
			strings.Contains(source, "InjectConnection") ||
			strings.Contains(source, "ChangeStream")
		if isMongoCtx {
			for _, m := range reChangeStream.FindAllStringSubmatch(source, -1) {
				receiver := strings.ToLower(m[1])
				// skip obvious false positives
				if receiver == "fs" || receiver == "chokidar" || receiver == "observable" || receiver == "rxjs" {
					continue
				}
				out = append(out, DataAccessRef{
					DBType:     "mongodb",
					AccessType: "change_stream",
					SourceFile: rel,
				})
			}
		}

		// Mongoose connections
		for _, m := range reMongoConnEnv.FindAllStringSubmatch(source, -1) {
			out = append(out, DataAccessRef{
				ConnectionID: m[1],
				DBType:       "mongodb",
				AccessType:   "connection",
				SourceFile:   rel,
			})
		}
		for _, m := range reUriEnv.FindAllStringSubmatch(source, -1) {
			// Only record if the surrounding context names Mongoose to avoid
			// picking up unrelated `uri: process.env.X` in other modules.
			if !strings.Contains(source, "MongooseModule") {
				continue
			}
			out = append(out, DataAccessRef{
				ConnectionID: m[1],
				DBType:       "mongodb",
				AccessType:   "connection",
				SourceFile:   rel,
			})
		}
		return nil
	})

	return out, err
}
