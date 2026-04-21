package infra

import (
	"testing"
)

func TestExtractDataAccess_MongooseSchemaDecorator(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/contacts/contact.schema.ts", `
import { Schema, Prop, SchemaFactory } from '@nestjs/mongoose';

@Schema({ collection: 'contacts', timestamps: true })
export class Contact {
  @Prop() locationId: string;
  @Prop() email: string;
}

export const ContactSchema = SchemaFactory.createForClass(Contact);
`)
	writeFile(t, root, "src/workflows/workflow.schema.ts", `
@Schema({ collection: 'workflows' })
export class Workflow {}
`)
	refs, err := ExtractDataAccess(root)
	if err != nil {
		t.Fatalf("ExtractDataAccess: %v", err)
	}
	// Expect two Schema rows (contacts, workflows), access_type = "write"
	// since @Schema implies ownership + write access.
	colls := map[string]DataAccessRef{}
	for _, r := range refs {
		if r.DBType == "mongodb" {
			colls[r.Collection] = r
		}
	}
	if len(colls) != 2 {
		t.Fatalf("want 2 mongo collections, got %d: %+v", len(colls), refs)
	}
	if colls["contacts"].AccessType != "write" {
		t.Errorf("contacts access_type: got %q, want write", colls["contacts"].AccessType)
	}
	if colls["workflows"].DBType != "mongodb" {
		t.Errorf("workflows db_type: got %q, want mongodb", colls["workflows"].DBType)
	}
}

func TestExtractDataAccess_MongoChangeStream(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/events/change-stream.service.ts", `
import { InjectConnection } from '@nestjs/mongoose';

@Injectable()
export class ContactChangeStream {
  async watch() {
    const stream = this.collection.watch([], { fullDocument: 'updateLookup' });
    stream.on('change', (change) => {});
  }
}
`)
	refs, err := ExtractDataAccess(root)
	if err != nil {
		t.Fatalf("ExtractDataAccess: %v", err)
	}
	var changeStreamRow *DataAccessRef
	for i := range refs {
		if refs[i].AccessType == "change_stream" {
			changeStreamRow = &refs[i]
			break
		}
	}
	if changeStreamRow == nil {
		t.Fatal("expected a change_stream row for collection.watch()")
	}
	if changeStreamRow.DBType != "mongodb" {
		t.Errorf("change_stream db_type: got %q, want mongodb", changeStreamRow.DBType)
	}
}

func TestExtractDataAccess_TypeOrmEntity(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/billing/invoice.entity.ts", `
import { Entity, Column, PrimaryGeneratedColumn } from 'typeorm';

@Entity({ name: 'invoices' })
export class Invoice {
  @PrimaryGeneratedColumn()
  id: number;

  @Column()
  locationId: string;
}
`)
	refs, err := ExtractDataAccess(root)
	if err != nil {
		t.Fatalf("ExtractDataAccess: %v", err)
	}
	var got *DataAccessRef
	for i := range refs {
		if refs[i].Collection == "invoices" {
			got = &refs[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("expected a typeorm entity row for invoices, got: %+v", refs)
	}
	if got.DBType != "postgres" {
		t.Errorf("DBType: got %q, want postgres", got.DBType)
	}
}

func TestExtractDataAccess_MongooseConnectionStringEnv(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/db/mongo.module.ts", `
import { MongooseModule } from '@nestjs/mongoose';

@Module({
  imports: [
    MongooseModule.forRootAsync({
      useFactory: () => ({
        uri: process.env.MONGO_URL,
      }),
    }),
    MongooseModule.forRoot(process.env.MONGO_URL_LABS_STANDARD),
  ],
})
export class AppModule {}
`)
	refs, err := ExtractDataAccess(root)
	if err != nil {
		t.Fatalf("ExtractDataAccess: %v", err)
	}
	// Expect two connection refs — one per forRoot call site, connection_id
	// = the env var name when it can be recovered.
	connections := map[string]bool{}
	for _, r := range refs {
		if r.AccessType == "connection" {
			connections[r.ConnectionID] = true
		}
	}
	if !connections["MONGO_URL"] {
		t.Errorf("missing MONGO_URL connection; got %v", connections)
	}
	if !connections["MONGO_URL_LABS_STANDARD"] {
		t.Errorf("missing MONGO_URL_LABS_STANDARD connection; got %v", connections)
	}
}

func TestExtractDataAccess_SkipsNodeModulesAndTests(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "node_modules/some-pkg/schema.ts", `@Schema({ collection: 'x' }) class X {}`)
	writeFile(t, root, "dist/schema.ts", `@Schema({ collection: 'y' }) class Y {}`)
	writeFile(t, root, "src/foo.spec.ts", `@Schema({ collection: 'z' }) class Z {}`)
	refs, err := ExtractDataAccess(root)
	if err != nil {
		t.Fatalf("ExtractDataAccess: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("want 0 refs, got %d: %+v", len(refs), refs)
	}
}
