package enricher

import (
	"testing"
)

// TestExtractVueComponent_ScriptSetup covers Vue 3 <script setup> syntax
// which is the dominant style across GHL frontends (2024+ code).
func TestExtractVueComponent_ScriptSetup(t *testing.T) {
	source := `
<template>
  <div class="user-permissions">
    <h1>{{ t('settings.users.permissions.title') }}</h1>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
const permissions = ref([])
</script>

<style scoped>
.user-permissions { padding: 1rem }
</style>
`
	meta, err := ExtractVueComponent(source, "apps/settings/src/components/user/UserPermissionsV2.vue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !meta.HasScriptSetup {
		t.Errorf("HasScriptSetup = false, want true")
	}
	if meta.ScriptLang != "ts" {
		t.Errorf("ScriptLang = %q, want %q", meta.ScriptLang, "ts")
	}
	if meta.HasTemplate != true {
		t.Errorf("HasTemplate = false, want true")
	}
	// Component name for <script setup> is derived from the filename.
	if meta.ComponentName != "UserPermissionsV2" {
		t.Errorf("ComponentName = %q, want %q", meta.ComponentName, "UserPermissionsV2")
	}
	if meta.FilePath != "apps/settings/src/components/user/UserPermissionsV2.vue" {
		t.Errorf("FilePath = %q", meta.FilePath)
	}
}

// TestExtractVueComponent_OptionsAPI covers Vue 2 / Vue 3 Options API with
// an explicit `name:` field inside `export default`.
func TestExtractVueComponent_OptionsAPI(t *testing.T) {
	source := `
<template>
  <div>legacy</div>
</template>

<script>
export default {
  name: 'UserView',
  data() {
    return { user: null }
  }
}
</script>
`
	meta, err := ExtractVueComponent(source, "apps/settings/src/components/user/UserView.vue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.HasScriptSetup {
		t.Errorf("HasScriptSetup = true, want false")
	}
	if meta.ComponentName != "UserView" {
		t.Errorf("ComponentName = %q, want %q", meta.ComponentName, "UserView")
	}
	if meta.ScriptLang != "js" {
		t.Errorf("ScriptLang = %q, want %q (default when lang attr omitted)", meta.ScriptLang, "js")
	}
}

// TestExtractVueComponent_DefineComponent covers the defineComponent helper
// pattern that's common in Vue 3 Composition API (but not script-setup).
func TestExtractVueComponent_DefineComponent(t *testing.T) {
	source := `
<template><div /></template>

<script lang="ts">
import { defineComponent } from 'vue'

export default defineComponent({
  name: 'LocationPicker',
  setup() {
    return {}
  }
})
</script>
`
	meta, err := ExtractVueComponent(source, "apps/conversations-components/src/LocationPicker.vue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ComponentName != "LocationPicker" {
		t.Errorf("ComponentName = %q, want %q", meta.ComponentName, "LocationPicker")
	}
	if meta.ScriptLang != "ts" {
		t.Errorf("ScriptLang = %q, want %q", meta.ScriptLang, "ts")
	}
}

// TestExtractVueComponent_FilenameFallback covers the case where neither
// <script setup> nor an explicit `name:` is present — we fall back to
// the filename stem.
func TestExtractVueComponent_FilenameFallback(t *testing.T) {
	source := `
<template><div /></template>
<script>
export default {}
</script>
`
	meta, err := ExtractVueComponent(source, "apps/x/my-widget.vue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Filename stem converted to PascalCase-like identifier.
	// Kebab-case "my-widget" → "MyWidget".
	if meta.ComponentName != "MyWidget" {
		t.Errorf("ComponentName = %q, want %q (kebab→Pascal of filename stem)", meta.ComponentName, "MyWidget")
	}
}

// TestExtractVueComponent_ExtractsI18nKeys pulls out $t('...') and t('...')
// calls from the <template> block. Used downstream to translate affected
// backend changes to user-visible UI strings.
func TestExtractVueComponent_ExtractsI18nKeys(t *testing.T) {
	source := `
<template>
  <div>
    <h1>{{ t('settings.users.permissions.title') }}</h1>
    <p>{{ $t('common.loading') }}</p>
    <button>{{ t('settings.users.permissions.save_button') }}</button>
  </div>
</template>

<script setup lang="ts">
const { t } = useI18n()
</script>
`
	meta, err := ExtractVueComponent(source, "apps/settings/UserPermissions.vue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantKeys := []string{"settings.users.permissions.title", "common.loading", "settings.users.permissions.save_button"}
	if len(meta.I18nKeys) != len(wantKeys) {
		t.Fatalf("I18nKeys = %v, want %v", meta.I18nKeys, wantKeys)
	}
	for i, want := range wantKeys {
		if meta.I18nKeys[i] != want {
			t.Errorf("I18nKeys[%d] = %q, want %q", i, meta.I18nKeys[i], want)
		}
	}
}

// TestExtractVueComponent_NotAVueFile returns a descriptive error for a
// source that has no <template> and no <script> — can't be a Vue SFC.
func TestExtractVueComponent_NotAVueFile(t *testing.T) {
	source := `const x = 1; export default x;`
	_, err := ExtractVueComponent(source, "not-really.vue")
	if err == nil {
		t.Fatalf("expected error for non-SFC source, got nil")
	}
}

// TestExtractVueComponent_EmptySource returns an error rather than panicking.
func TestExtractVueComponent_EmptySource(t *testing.T) {
	_, err := ExtractVueComponent("", "empty.vue")
	if err == nil {
		t.Fatalf("expected error for empty source, got nil")
	}
}
