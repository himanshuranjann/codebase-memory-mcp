package enricher

import (
	"context"
	"regexp"
	"strings"
)

// MongoSearcher searches org-wide for MongoDB query patterns.
// Satisfied at runtime by *searchtools.OrgSearch; use a mock in tests.
type MongoSearcher interface {
	SearchAll(ctx context.Context, pattern, fileGlob string) ([]MongoSearchHit, error)
}

// MongoSearchHit is one file-level match from a MongoDB pattern search.
type MongoSearchHit struct {
	Repo     string
	FilePath string
	Line     int
	Text     string
}

// MongoModelDef is a Mongoose model definition extracted from source.
type MongoModelDef struct {
	ModelName      string // e.g. "CommunityOffer"
	CollectionName string // e.g. "communityoffer"
	FilePath       string
}

// MongoReaderResult describes a repo that queries the same MongoDB collection.
type MongoReaderResult struct {
	CollectionName string
	ModelName      string
	ReaderRepo     string
	FilePath       string
	QueryPatterns  []string // e.g. ["find", "aggregate"]
	MFAAppKeys     []string
	IsCrossService bool // true when reader repo differs from the model's repo
}

var (
	reMongooseModel = regexp.MustCompile(`mongoose\.model\(\s*['"](\w+)['"]`)
	reGenericModel  = regexp.MustCompile(`model<\w+>\(\s*['"](\w+)['"]`)
	reSchemaClass   = regexp.MustCompile(`(?s)@Schema\(\)[\s\S]{0,200}export\s+class\s+(\w+)`)
	reQueryOps      = regexp.MustCompile(`\.(find|findOne|aggregate|updateOne|updateMany|deleteOne|deleteMany|countDocuments|distinct)\s*\(`)
)

// ExtractMongoModels extracts Mongoose model definitions from TypeScript source.
// Detects mongoose.model('Name'), model<T>('Name'), and @Schema() decorated classes.
func ExtractMongoModels(source, filePath string) []MongoModelDef {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	seen := make(map[string]bool)
	var models []MongoModelDef

	add := func(modelName, collectionName string) {
		if modelName == "" {
			return
		}
		if collectionName == "" {
			collectionName = strings.ToLower(modelName)
		}
		key := modelName + "|" + collectionName
		if seen[key] {
			return
		}
		seen[key] = true
		models = append(models, MongoModelDef{
			ModelName:      modelName,
			CollectionName: collectionName,
			FilePath:       filePath,
		})
	}

	for _, m := range reMongooseModel.FindAllStringSubmatch(source, -1) {
		add(m[1], strings.ToLower(m[1]))
	}
	for _, m := range reGenericModel.FindAllStringSubmatch(source, -1) {
		add(m[1], strings.ToLower(m[1]))
	}
	for _, m := range reSchemaClass.FindAllStringSubmatch(source, -1) {
		add(m[1], strings.ToLower(m[1]))
	}
	return models
}

// TraceMongoReaders finds all repos that query the same MongoDB collections
// as the models in models. Cross-service reads are flagged with IsCrossService=true.
// Returns nil when no models are found or searcher is nil.
func TraceMongoReaders(ctx context.Context, models []MongoModelDef, sourceRepo string, mfaReg *MFARegistry, searcher MongoSearcher) ([]MongoReaderResult, error) {
	if searcher == nil || len(models) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	var results []MongoReaderResult

	for _, model := range models {
		// Pattern matches either the collection name as a string OR the model
		// variable with a query method chained on it.
		pattern := `['"]` + regexp.QuoteMeta(model.CollectionName) + `['"]|` +
			regexp.QuoteMeta(model.ModelName) + `\.(find|aggregate|findOne|updateOne|deleteOne|countDocuments)`
		hits, err := searcher.SearchAll(ctx, pattern, "*.{ts,js}")
		if err != nil {
			continue
		}
		for _, hit := range hits {
			if isTestFile(hit.FilePath) {
				continue
			}
			if hit.FilePath == model.FilePath {
				continue
			}
			key := model.CollectionName + "|" + hit.Repo + "|" + hit.FilePath
			if seen[key] {
				continue
			}
			seen[key] = true

			var queries []string
			for _, qm := range reQueryOps.FindAllStringSubmatch(hit.Text, -1) {
				queries = append(queries, qm[1])
			}

			var mfaKeys []string
			if mfaReg != nil {
				for _, app := range mfaReg.LookupByRepo(hit.Repo) {
					mfaKeys = append(mfaKeys, app.Key)
				}
			}

			results = append(results, MongoReaderResult{
				CollectionName: model.CollectionName,
				ModelName:      model.ModelName,
				ReaderRepo:     hit.Repo,
				FilePath:       hit.FilePath,
				QueryPatterns:  queries,
				MFAAppKeys:     mfaKeys,
				IsCrossService: hit.Repo != sourceRepo,
			})
		}
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results, nil
}
