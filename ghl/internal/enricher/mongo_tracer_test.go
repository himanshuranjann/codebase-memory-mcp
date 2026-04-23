package enricher

import (
	"context"
	"testing"
)

type mockMongoSearcher struct {
	hits []MongoSearchHit
}

func (m *mockMongoSearcher) SearchAll(_ context.Context, _, _ string) ([]MongoSearchHit, error) {
	return m.hits, nil
}

func TestExtractMongoModels_DetectsMongooseModel(t *testing.T) {
	src := `const CommunityOffer = mongoose.model('CommunityOffer', schema);`
	got := ExtractMongoModels(src, "f.ts")
	if len(got) != 1 || got[0].ModelName != "CommunityOffer" {
		t.Errorf("got %+v", got)
	}
	if got[0].CollectionName != "communityoffer" {
		t.Errorf("CollectionName = %q", got[0].CollectionName)
	}
}

func TestExtractMongoModels_DetectsNestJSSchemaDecorator(t *testing.T) {
	src := `@Schema()
export class CommunityOffer extends Document {}`
	got := ExtractMongoModels(src, "f.ts")
	if len(got) == 0 {
		t.Fatal("expected NestJS @Schema class to be detected")
	}
	if got[0].ModelName != "CommunityOffer" {
		t.Errorf("ModelName = %q", got[0].ModelName)
	}
}

func TestExtractMongoModels_DetectsGenericModel(t *testing.T) {
	src := `model<ICommunityOffer>('community_offers', schema)`
	got := ExtractMongoModels(src, "f.ts")
	if len(got) == 0 {
		t.Fatal("expected generic model<T> to be detected")
	}
	if got[0].CollectionName != "community_offers" {
		t.Errorf("CollectionName = %q", got[0].CollectionName)
	}
}

func TestExtractMongoModels_EmptySource_ReturnsNil(t *testing.T) {
	if got := ExtractMongoModels("", "f.ts"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestTraceMongoReaders_NilSearcher_ReturnsNil(t *testing.T) {
	models := []MongoModelDef{{ModelName: "X", CollectionName: "x"}}
	got, _ := TraceMongoReaders(context.Background(), models, "repo", nil, nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestTraceMongoReaders_NoModels_ReturnsNil(t *testing.T) {
	searcher := &mockMongoSearcher{hits: []MongoSearchHit{{Repo: "r"}}}
	got, _ := TraceMongoReaders(context.Background(), nil, "repo", nil, searcher)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestTraceMongoReaders_FindsCrossServiceReader(t *testing.T) {
	searcher := &mockMongoSearcher{hits: []MongoSearchHit{
		{Repo: "ghl-communities-backend", FilePath: "src/offer.service.ts",
			Text: "CommunityOffer.find({groupId})"},
	}}
	models := []MongoModelDef{{ModelName: "CommunityOffer", CollectionName: "communityoffer", FilePath: "orig.ts"}}
	results, _ := TraceMongoReaders(context.Background(), models, "ghl-revex-backend", nil, searcher)
	if len(results) == 0 {
		t.Fatal("expected at least 1 reader result")
	}
	if !results[0].IsCrossService {
		t.Errorf("expected IsCrossService=true for different repo")
	}
	if results[0].ReaderRepo != "ghl-communities-backend" {
		t.Errorf("ReaderRepo = %q", results[0].ReaderRepo)
	}
}

func TestTraceMongoReaders_SameRepoNotCrossService(t *testing.T) {
	searcher := &mockMongoSearcher{hits: []MongoSearchHit{
		{Repo: "ghl-revex-backend", FilePath: "src/other.service.ts",
			Text: "CommunityOffer.findOne({})"},
	}}
	models := []MongoModelDef{{ModelName: "CommunityOffer", CollectionName: "communityoffer", FilePath: "orig.ts"}}
	results, _ := TraceMongoReaders(context.Background(), models, "ghl-revex-backend", nil, searcher)
	if len(results) == 0 {
		t.Fatal("expected result even for same-repo reader")
	}
	if results[0].IsCrossService {
		t.Errorf("expected IsCrossService=false for same repo")
	}
}

func TestTraceMongoReaders_SkipsTestFiles(t *testing.T) {
	searcher := &mockMongoSearcher{hits: []MongoSearchHit{
		{Repo: "ghl-x-backend", FilePath: "src/offer.spec.ts", Text: "CommunityOffer.find({})"},
	}}
	models := []MongoModelDef{{ModelName: "CommunityOffer", CollectionName: "communityoffer", FilePath: "o.ts"}}
	results, _ := TraceMongoReaders(context.Background(), models, "src", nil, searcher)
	if len(results) != 0 {
		t.Errorf("expected test file to be skipped, got %d", len(results))
	}
}

func TestTraceMongoReaders_ExtractsQueryPatterns(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"CommunityOffer.find({})", "find"},
		{"CommunityOffer.aggregate([])", "aggregate"},
		{"CommunityOffer.findOne({})", "findOne"},
		{"CommunityOffer.updateOne({})", "updateOne"},
	}
	for _, tt := range tests {
		searcher := &mockMongoSearcher{hits: []MongoSearchHit{
			{Repo: "r", FilePath: "f.ts", Text: tt.text},
		}}
		models := []MongoModelDef{{ModelName: "CommunityOffer", CollectionName: "communityoffer", FilePath: "o.ts"}}
		results, _ := TraceMongoReaders(context.Background(), models, "src", nil, searcher)
		if len(results) == 0 {
			t.Errorf("no results for %q", tt.text)
			continue
		}
		found := false
		for _, q := range results[0].QueryPatterns {
			if q == tt.want {
				found = true
			}
		}
		if !found {
			t.Errorf("QueryPatterns for %q = %v, want %q", tt.text, results[0].QueryPatterns, tt.want)
		}
	}
}

func TestTraceMongoReaders_DeduplicatesByRepoAndFile(t *testing.T) {
	searcher := &mockMongoSearcher{hits: []MongoSearchHit{
		{Repo: "r", FilePath: "f.ts", Text: "X.find({})"},
		{Repo: "r", FilePath: "f.ts", Text: "X.find({})"},
		{Repo: "r", FilePath: "f.ts", Text: "X.find({})"},
	}}
	models := []MongoModelDef{{ModelName: "X", CollectionName: "x", FilePath: "o.ts"}}
	results, _ := TraceMongoReaders(context.Background(), models, "src", nil, searcher)
	if len(results) != 1 {
		t.Errorf("expected deduplicated to 1, got %d", len(results))
	}
}
