package enricher

import (
	"testing"
)

// ---------------------------------------------------------------------------
// ExtractDTOMetadata tests
// ---------------------------------------------------------------------------

func TestExtractDTOMetadata_BasicFields(t *testing.T) {
	source := `
import { IsString, IsOptional, IsNotEmpty } from 'class-validator';

export class CreateContactDto {
  @IsNotEmpty()
  @IsString()
  firstName: string;

  @IsOptional()
  @IsString()
  lastName?: string;

  @IsString()
  email: string;
}
`
	metas := ExtractDTOMetadata(source, "src/contacts/dto/create-contact.dto.ts")
	if len(metas) != 1 {
		t.Fatalf("expected 1 DTO class, got %d", len(metas))
	}
	m := metas[0]
	if m.ClassName != "CreateContactDto" {
		t.Errorf("ClassName = %q, want CreateContactDto", m.ClassName)
	}
	if m.FilePath != "src/contacts/dto/create-contact.dto.ts" {
		t.Errorf("FilePath = %q", m.FilePath)
	}

	byName := map[string]DTOField{}
	for _, f := range m.Fields {
		byName[f.Name] = f
	}

	// firstName: required (no ? and no @IsOptional)
	fn, ok := byName["firstName"]
	if !ok {
		t.Fatal("field 'firstName' not found")
	}
	if !fn.Required {
		t.Error("firstName should be required")
	}
	if fn.TypeName != "string" {
		t.Errorf("firstName TypeName = %q, want string", fn.TypeName)
	}

	// lastName: optional via @IsOptional decorator
	ln, ok := byName["lastName"]
	if !ok {
		t.Fatal("field 'lastName' not found")
	}
	if ln.Required {
		t.Error("lastName should be optional (@IsOptional present)")
	}

	// email: required (no ? and no @IsOptional)
	em, ok := byName["email"]
	if !ok {
		t.Fatal("field 'email' not found")
	}
	if !em.Required {
		t.Error("email should be required")
	}
}

func TestExtractDTOMetadata_OptionalTsMarker(t *testing.T) {
	source := `
export class UpdateContactDto {
  name?: string;
  phone: string;
}
`
	metas := ExtractDTOMetadata(source, "update-contact.dto.ts")
	if len(metas) != 1 {
		t.Fatalf("expected 1 class, got %d", len(metas))
	}
	byName := map[string]DTOField{}
	for _, f := range metas[0].Fields {
		byName[f.Name] = f
	}
	if byName["name"].Required {
		t.Error("name should be optional (has '?')")
	}
	if !byName["phone"].Required {
		t.Error("phone should be required (no '?' and no @IsOptional)")
	}
}

func TestExtractDTOMetadata_NoClassReturnsNil(t *testing.T) {
	source := `
const foo = 'bar';
function doSomething() {}
`
	metas := ExtractDTOMetadata(source, "some.service.ts")
	if metas != nil {
		t.Errorf("expected nil for non-DTO source, got %v", metas)
	}
}

func TestExtractDTOMetadata_EmptySourceReturnsNil(t *testing.T) {
	metas := ExtractDTOMetadata("", "create.dto.ts")
	if metas != nil {
		t.Errorf("expected nil for empty source, got %v", metas)
	}
}

func TestExtractDTOMetadata_MultipleClasses(t *testing.T) {
	source := `
export class CreateUserDto {
  name: string;
}

export class UpdateUserDto {
  name?: string;
}
`
	metas := ExtractDTOMetadata(source, "user.dto.ts")
	if len(metas) != 2 {
		t.Fatalf("expected 2 classes, got %d", len(metas))
	}
	names := map[string]bool{}
	for _, m := range metas {
		names[m.ClassName] = true
	}
	if !names["CreateUserDto"] || !names["UpdateUserDto"] {
		t.Errorf("class names = %v", names)
	}
}

func TestExtractDTOMetadata_ComplexTypes(t *testing.T) {
	source := `
export class OrderDto {
  items: string[];
  metadata: Record<string, unknown>;
  status: 'active' | 'inactive';
}
`
	metas := ExtractDTOMetadata(source, "order.dto.ts")
	if len(metas) != 1 {
		t.Fatalf("expected 1 class, got %d", len(metas))
	}
	byName := map[string]DTOField{}
	for _, f := range metas[0].Fields {
		byName[f.Name] = f
	}
	if byName["items"].TypeName == "" {
		t.Error("items.TypeName should not be empty")
	}
}

// ---------------------------------------------------------------------------
// DiffDTOSchema tests
// ---------------------------------------------------------------------------

func TestDiffDTOSchema_NoChanges(t *testing.T) {
	base := DTOMetadata{
		ClassName: "FooDto",
		Fields: []DTOField{
			{Name: "name", TypeName: "string", Required: true},
			{Name: "age", TypeName: "number", Required: false},
		},
	}
	head := DTOMetadata{
		ClassName: "FooDto",
		Fields: []DTOField{
			{Name: "name", TypeName: "string", Required: true},
			{Name: "age", TypeName: "number", Required: false},
		},
	}
	deltas := DiffDTOSchema(base, head)
	if len(deltas) != 0 {
		t.Errorf("expected 0 deltas, got %d: %v", len(deltas), deltas)
	}
}

func TestDiffDTOSchema_FieldRemoved_Breaking(t *testing.T) {
	base := DTOMetadata{Fields: []DTOField{{Name: "email", TypeName: "string", Required: true}}}
	head := DTOMetadata{Fields: nil}
	deltas := DiffDTOSchema(base, head)
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	d := deltas[0]
	if d.Kind != ContractFieldRemoved {
		t.Errorf("Kind = %q, want FIELD_REMOVED", d.Kind)
	}
	if !d.Breaking {
		t.Error("FIELD_REMOVED should be Breaking=true")
	}
	if d.FieldName != "email" {
		t.Errorf("FieldName = %q, want email", d.FieldName)
	}
}

func TestDiffDTOSchema_RequiredFieldAdded_Breaking(t *testing.T) {
	base := DTOMetadata{Fields: nil}
	head := DTOMetadata{Fields: []DTOField{{Name: "phone", TypeName: "string", Required: true}}}
	deltas := DiffDTOSchema(base, head)
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	d := deltas[0]
	if d.Kind != ContractRequiredFieldAdded {
		t.Errorf("Kind = %q, want REQUIRED_FIELD_ADDED", d.Kind)
	}
	if !d.Breaking {
		t.Error("REQUIRED_FIELD_ADDED should be Breaking=true")
	}
}

func TestDiffDTOSchema_OptionalFieldAdded_NotBreaking(t *testing.T) {
	base := DTOMetadata{Fields: nil}
	head := DTOMetadata{Fields: []DTOField{{Name: "nickname", TypeName: "string", Required: false}}}
	deltas := DiffDTOSchema(base, head)
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	d := deltas[0]
	if d.Kind != ContractOptionalFieldAdded {
		t.Errorf("Kind = %q, want OPTIONAL_FIELD_ADDED", d.Kind)
	}
	if d.Breaking {
		t.Error("OPTIONAL_FIELD_ADDED should be Breaking=false")
	}
}

func TestDiffDTOSchema_TypeChanged_Breaking(t *testing.T) {
	base := DTOMetadata{Fields: []DTOField{{Name: "count", TypeName: "string", Required: true}}}
	head := DTOMetadata{Fields: []DTOField{{Name: "count", TypeName: "number", Required: true}}}
	deltas := DiffDTOSchema(base, head)
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	d := deltas[0]
	if d.Kind != ContractTypeChanged {
		t.Errorf("Kind = %q, want TYPE_CHANGED", d.Kind)
	}
	if !d.Breaking {
		t.Error("TYPE_CHANGED should be Breaking=true")
	}
	if d.OldType != "string" || d.NewType != "number" {
		t.Errorf("OldType=%q NewType=%q", d.OldType, d.NewType)
	}
}

func TestDiffDTOSchema_OptionalMadeRequired_Breaking(t *testing.T) {
	base := DTOMetadata{Fields: []DTOField{{Name: "meta", TypeName: "string", Required: false}}}
	head := DTOMetadata{Fields: []DTOField{{Name: "meta", TypeName: "string", Required: true}}}
	deltas := DiffDTOSchema(base, head)
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	d := deltas[0]
	if d.Kind != ContractOptionalMadeRequired {
		t.Errorf("Kind = %q, want OPTIONAL_MADE_REQUIRED", d.Kind)
	}
	if !d.Breaking {
		t.Error("OPTIONAL_MADE_REQUIRED should be Breaking=true")
	}
}

func TestDiffDTOSchema_RequiredMadeOptional_NotBreaking(t *testing.T) {
	base := DTOMetadata{Fields: []DTOField{{Name: "notes", TypeName: "string", Required: true}}}
	head := DTOMetadata{Fields: []DTOField{{Name: "notes", TypeName: "string", Required: false}}}
	deltas := DiffDTOSchema(base, head)
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}
	d := deltas[0]
	if d.Kind != ContractRequiredMadeOptional {
		t.Errorf("Kind = %q, want REQUIRED_MADE_OPTIONAL", d.Kind)
	}
	if d.Breaking {
		t.Error("REQUIRED_MADE_OPTIONAL should be Breaking=false")
	}
}

func TestDiffDTOSchema_MultipleDeltas(t *testing.T) {
	base := DTOMetadata{
		Fields: []DTOField{
			{Name: "a", TypeName: "string", Required: true},  // removed
			{Name: "b", TypeName: "string", Required: false}, // type changed
		},
	}
	head := DTOMetadata{
		Fields: []DTOField{
			{Name: "b", TypeName: "number", Required: false}, // type changed
			{Name: "c", TypeName: "boolean", Required: false}, // added optional
		},
	}
	deltas := DiffDTOSchema(base, head)
	if len(deltas) != 3 {
		t.Fatalf("expected 3 deltas, got %d: %v", len(deltas), deltas)
	}
	breaking := 0
	for _, d := range deltas {
		if d.Breaking {
			breaking++
		}
	}
	if breaking != 2 {
		// FIELD_REMOVED + TYPE_CHANGED are breaking; OPTIONAL_FIELD_ADDED is not
		t.Errorf("expected 2 breaking deltas, got %d", breaking)
	}
}
