// Package infra extracts infrastructure-level signals (Helm, Istio, data
// access) from a repository root. These populate the orgdb tables that
// are schema-defined but not currently filled in by the TypeScript
// enricher: deployments, service_mesh, shared_databases.
package infra

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// DeploymentRef describes one deployable unit — typically one Helm values
// file under apps/<app>/deployments/<env>/, or a Chart.yaml when the repo
// is itself a single-app chart.
type DeploymentRef struct {
	RepoName   string // filled in by caller; left empty here
	AppName    string
	DeployType string // "deployment" (default) | "worker" | "statefulset" | "cronjob" | "daemonset"
	Env        string // "staging" | "production" | "dev" | ""
	Namespace  string
	HelmChart  string
	Team       string
	SubTeam    string
	SourceFile string // relative path inside repo
}

// skippedDirs are directories we never descend into while walking for
// helm / k8s YAMLs.
var skippedDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"dist":         true,
	"build":        true,
	"coverage":     true,
	".next":        true,
	".nuxt":        true,
	"tmp":          true,
}

// helm file-name regex: values*.yaml or Chart.yaml — never starting with a dot.
var reHelmFile = regexp.MustCompile(`^(values[^/]*\.ya?ml|Chart\.ya?ml)$`)

// reEnvFromPath pulls /staging/ or /production/ or /dev/ from a path.
var reEnvFromPath = regexp.MustCompile(`/(staging|production|prod|dev|development|qa)/`)

// reEnvFromFile pulls staging|production from a filename like
// values-staging.yaml or values.server.production.yaml.
var reEnvFromFile = regexp.MustCompile(`(?:-|\.)(staging|production|prod|dev|development|qa)\.ya?ml$`)

// ExtractDeployments walks the repo root and returns one DeploymentRef
// per helm values file or Chart.yaml encountered. Team / sub-team / app
// come from the `labels:` block that GHL monorepos standardize on.
func ExtractDeployments(root string) ([]DeploymentRef, error) {
	var out []DeploymentRef

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if skippedDirs[name] || strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if !reHelmFile.MatchString(name) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)

		// Chart.yaml — emit a single chart-level DeploymentRef.
		if name == "Chart.yaml" || name == "Chart.yml" {
			ref, ok := parseChartFile(path, rel)
			if ok {
				out = append(out, ref)
			}
			return nil
		}

		// values*.yaml — emit one ref per file.
		ref, ok := parseValuesFile(path, rel)
		if ok {
			out = append(out, ref)
		}
		return nil
	})

	return out, err
}

// chartDoc is the Chart.yaml shape we care about.
type chartDoc struct {
	APIVersion string `yaml:"apiVersion"`
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	AppVersion string `yaml:"appVersion"`
}

func parseChartFile(path, rel string) (DeploymentRef, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DeploymentRef{}, false
	}
	var c chartDoc
	if err := yaml.Unmarshal(data, &c); err != nil {
		return DeploymentRef{}, false
	}
	if c.Name == "" {
		return DeploymentRef{}, false
	}
	return DeploymentRef{
		AppName:    c.Name,
		DeployType: "deployment",
		HelmChart:  c.Name,
		SourceFile: rel,
	}, true
}

// valuesDoc is the loose shape of a GHL values file. Everything is
// optional — we probe for each field independently.
type valuesDoc struct {
	Environment string            `yaml:"environment"`
	Namespace   string            `yaml:"namespace"`
	Labels      map[string]string `yaml:"labels"`
	Image       struct {
		Repository string `yaml:"repository"`
	} `yaml:"image"`
}

func parseValuesFile(path, rel string) (DeploymentRef, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DeploymentRef{}, false
	}
	var v valuesDoc
	if err := yaml.Unmarshal(data, &v); err != nil {
		return DeploymentRef{}, false
	}

	ref := DeploymentRef{
		SourceFile: rel,
		Namespace:  v.Namespace,
	}

	// Env: prefer explicit field, then filename, then path.
	ref.Env = v.Environment
	if ref.Env == "" {
		if m := reEnvFromFile.FindStringSubmatch(rel); m != nil {
			ref.Env = normalizeEnv(m[1])
		}
	}
	if ref.Env == "" {
		if m := reEnvFromPath.FindStringSubmatch("/" + rel); m != nil {
			ref.Env = normalizeEnv(m[1])
		}
	}

	// Team / sub-team / app / deploy-type from the labels block.
	ref.Team = v.Labels["team"]
	ref.SubTeam = v.Labels["subTeam"]
	ref.AppName = firstNonEmpty(v.Labels["app"], v.Labels["DeploymentName"], v.Labels["product"])
	ref.DeployType = normalizeDeployType(v.Labels["deploymentType"])

	// If the file has no labels at all AND no app name could be inferred,
	// it's not describing a deployable — skip (this filters test values
	// files and partial overrides).
	if ref.AppName == "" && ref.Team == "" && ref.HelmChart == "" {
		return DeploymentRef{}, false
	}

	// If AppName is still empty but we have a filename like
	// values.<name>.<env>.yaml, extract the middle segment.
	if ref.AppName == "" {
		base := filepath.Base(rel)
		trimmed := strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")
		parts := strings.Split(trimmed, ".")
		if len(parts) >= 3 && parts[0] == "values" {
			ref.AppName = parts[1]
		}
	}

	return ref, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func normalizeEnv(e string) string {
	switch strings.ToLower(e) {
	case "prod":
		return "production"
	case "development":
		return "dev"
	default:
		return strings.ToLower(e)
	}
}

// normalizeDeployType maps GHL labels.deploymentType onto the shorter set
// the deployments table expects. Default is "deployment".
func normalizeDeployType(s string) string {
	switch strings.ToLower(s) {
	case "worker":
		return "worker"
	case "statefulset", "stateful":
		return "statefulset"
	case "cronjob", "cron":
		return "cronjob"
	case "daemonset":
		return "daemonset"
	default:
		return "deployment"
	}
}
