package infra

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// VirtualService is one row for the service_mesh table — derived from an
// Istio VirtualService manifest. Each <source → destination.host> pair in
// the spec.http[].route slice becomes its own row so the blast-radius /
// trace-flow queries can see fan-out.
type VirtualService struct {
	RepoName   string
	SourceApp  string // VirtualService name (or name of the gateway)
	TargetFQDN string // destination.host (e.g. foo.bar.svc.cluster.local)
	TargetRepo string // resolved later — caller sets from TargetFQDN
	Env        string
	SourceFile string
}

// minimal shape we parse. We intentionally don't model the full Istio
// schema — just enough to pull sourceApp + targetFQDN pairs.
type istioVS struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Hosts []string `yaml:"hosts"`
		HTTP  []struct {
			Route []struct {
				Destination struct {
					Host string `yaml:"host"`
				} `yaml:"destination"`
			} `yaml:"route"`
		} `yaml:"http"`
	} `yaml:"spec"`
}

// ExtractIstioVirtualServices walks the repo and yields one row per
// http[].route destination inside any file that contains a
// `kind: VirtualService` document (supports multi-doc YAML).
func ExtractIstioVirtualServices(root string) ([]VirtualService, error) {
	var out []VirtualService

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
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// Quick early-exit: only parse files that actually contain
		// "kind: VirtualService".
		if !bytes.Contains(data, []byte("kind: VirtualService")) {
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		env := ""
		if m := reEnvFromPath.FindStringSubmatch("/" + rel); m != nil {
			env = normalizeEnv(m[1])
		}

		dec := yaml.NewDecoder(bytes.NewReader(data))
		for {
			var doc istioVS
			if err := dec.Decode(&doc); err != nil {
				if err == io.EOF {
					break
				}
				break // malformed file — stop this decoder
			}
			if doc.Kind != "VirtualService" {
				continue
			}
			for _, h := range doc.Spec.HTTP {
				for _, r := range h.Route {
					if r.Destination.Host == "" {
						continue
					}
					out = append(out, VirtualService{
						SourceApp:  doc.Metadata.Name,
						TargetFQDN: r.Destination.Host,
						Env:        env,
						SourceFile: rel,
					})
				}
			}
		}
		return nil
	})

	return out, err
}
