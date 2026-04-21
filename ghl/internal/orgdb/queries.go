package orgdb

import "fmt"

// DependencyResult represents a package dependency relationship.
type DependencyResult struct {
	RepoName    string
	Scope       string
	PackageName string
	DepType     string
	VersionSpec string
}

// BlastRadiusResult represents the cross-repo impact of a change.
type BlastRadiusResult struct {
	AffectedRepos []AffectedRepo
	TotalRepos    int
}

// AffectedRepo is one repo affected in a blast radius analysis.
type AffectedRepo struct {
	Name       string
	Team       string
	Reason     string // "depends_on_package", "api_consumer", "event_consumer"
	Confidence float64
}

// FlowStep represents one hop in a cross-service flow trace.
type FlowStep struct {
	FromRepo   string
	ToRepo     string
	EdgeType   string // "api_contract", "event_contract", "package_dep"
	Detail     string // path or topic name
	Confidence float64
}

// TeamInfo represents a team's topology in the org.
type TeamInfo struct {
	Team     string
	Repos    []RepoSummary
	DepTeams []string // teams this team depends on
}

// RepoSummary is a brief description of a repo within a team.
type RepoSummary struct {
	Name      string
	Type      string
	NodeCount int
	EdgeCount int
}

// RepoSearchResult represents a repo found by search.
type RepoSearchResult struct {
	Name      string
	Team      string
	Type      string
	Languages string
	Score     float64
	Reason    string
}

// QueryDependents finds all repos that depend on a specific package.
// Returns an empty slice (not nil) when no repos match, so JSON marshals
// as [] instead of null.
func (d *DB) QueryDependents(packageScope, packageName string) ([]DependencyResult, error) {
	rows, err := d.db.Query(`
		SELECT r.name, p.scope, p.name, rd.dep_type, rd.version_spec
		FROM repo_dependencies rd
		JOIN repos r ON rd.repo_id = r.id
		JOIN packages p ON rd.package_id = p.id
		WHERE p.scope = ? AND p.name = ?
		ORDER BY r.name
	`, packageScope, packageName)
	if err != nil {
		return nil, fmt.Errorf("orgdb: query dependents %s/%s: %w", packageScope, packageName, err)
	}
	defer rows.Close()

	results := []DependencyResult{}
	for rows.Next() {
		var r DependencyResult
		if err := rows.Scan(&r.RepoName, &r.Scope, &r.PackageName, &r.DepType, &r.VersionSpec); err != nil {
			return nil, fmt.Errorf("orgdb: scan dependent: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// QueryBlastRadius finds all repos affected by a change in the given repo.
// It checks package dependents, API consumers, and event consumers.
func (d *DB) QueryBlastRadius(repoName string) (BlastRadiusResult, error) {
	// Exclude the repo itself from its own blast radius — this handles
	// both "a repo depends on its own published package" (rare but
	// possible for platform-* packages) and any lingering self-matched
	// contracts in historical data.
	rows, err := d.db.Query(`
		SELECT DISTINCT affected.name, COALESCE(NULLIF(r.team, ''), t.team, '') AS team, affected.reason FROM (
			SELECT DISTINCT r.name AS name, 'depends_on_package' as reason
			FROM repo_dependencies rd
			JOIN repos r ON rd.repo_id = r.id
			JOIN packages p ON rd.package_id = p.id
			WHERE p.provider_repo = ? AND r.name != ?

			UNION

			SELECT DISTINCT consumer_repo AS name, 'api_consumer'
			FROM api_contracts
			WHERE provider_repo = ?
			  AND consumer_repo IS NOT NULL
			  AND consumer_repo != ''
			  AND consumer_repo != provider_repo

			UNION

			SELECT DISTINCT consumer_repo AS name, 'event_consumer'
			FROM event_contracts
			WHERE producer_repo = ?
			  AND consumer_repo IS NOT NULL
			  AND consumer_repo != ''
			  AND consumer_repo != producer_repo
		) affected
		LEFT JOIN repos r ON r.name = affected.name
		LEFT JOIN team_ownership t ON t.repo_name = affected.name
		ORDER BY affected.name
	`, repoName, repoName, repoName, repoName)
	if err != nil {
		return BlastRadiusResult{}, fmt.Errorf("orgdb: query blast radius %q: %w", repoName, err)
	}
	defer rows.Close()

	result := BlastRadiusResult{AffectedRepos: []AffectedRepo{}}
	for rows.Next() {
		var ar AffectedRepo
		if err := rows.Scan(&ar.Name, &ar.Team, &ar.Reason); err != nil {
			return BlastRadiusResult{}, fmt.Errorf("orgdb: scan blast radius: %w", err)
		}
		ar.Confidence = 1.0
		result.AffectedRepos = append(result.AffectedRepos, ar)
	}
	if err := rows.Err(); err != nil {
		return BlastRadiusResult{}, err
	}
	result.TotalRepos = len(result.AffectedRepos)
	return result, nil
}

// TraceFlow traces a flow starting from a trigger repo.
// direction: "downstream" (who does this call) or "upstream" (who calls this).
// maxHops limits recursion depth (default 3, max 4).
func (d *DB) TraceFlow(trigger string, direction string, maxHops int) ([]FlowStep, error) {
	if maxHops <= 0 {
		maxHops = 3
	}
	if maxHops > 4 {
		maxHops = 4
	}

	// Self-loop filters (provider_repo != consumer_repo / producer_repo !=
	// consumer_repo) are applied at every level so that historical rows
	// written before the cross-reference fix can't pollute trace output.
	var query string
	if direction == "upstream" {
		query = `
			WITH RECURSIVE flow(from_repo, to_repo, edge_type, detail, confidence, depth) AS (
				SELECT provider_repo, consumer_repo, 'api_contract', path, confidence, 1
				FROM api_contracts
				WHERE consumer_repo = ? AND provider_repo != '' AND provider_repo != consumer_repo
				UNION ALL
				SELECT producer_repo, consumer_repo, 'event_contract', topic, 1.0, 1
				FROM event_contracts
				WHERE consumer_repo = ? AND producer_repo != '' AND producer_repo != consumer_repo
				UNION ALL
				SELECT ac.provider_repo, f.from_repo, 'api_contract', ac.path, ac.confidence, f.depth + 1
				FROM flow f
				JOIN api_contracts ac ON ac.consumer_repo = f.from_repo
				WHERE f.depth < ?
				  AND ac.provider_repo != ''
				  AND ac.provider_repo != f.to_repo
				  AND ac.provider_repo != ac.consumer_repo
				UNION ALL
				SELECT ec.producer_repo, f.from_repo, 'event_contract', ec.topic, 1.0, f.depth + 1
				FROM flow f
				JOIN event_contracts ec ON ec.consumer_repo = f.from_repo
				WHERE f.depth < ?
				  AND ec.producer_repo != ''
				  AND ec.producer_repo != f.to_repo
				  AND ec.producer_repo != ec.consumer_repo
			)
			SELECT DISTINCT from_repo, to_repo, edge_type, detail, confidence FROM flow
		`
	} else {
		query = `
			WITH RECURSIVE flow(from_repo, to_repo, edge_type, detail, confidence, depth) AS (
				SELECT provider_repo, consumer_repo, 'api_contract', path, confidence, 1
				FROM api_contracts
				WHERE provider_repo = ? AND consumer_repo != '' AND consumer_repo != provider_repo
				UNION ALL
				SELECT producer_repo, consumer_repo, 'event_contract', topic, 1.0, 1
				FROM event_contracts
				WHERE producer_repo = ? AND consumer_repo != '' AND consumer_repo != producer_repo
				UNION ALL
				SELECT f.to_repo, ac.consumer_repo, 'api_contract', ac.path, ac.confidence, f.depth + 1
				FROM flow f
				JOIN api_contracts ac ON ac.provider_repo = f.to_repo
				WHERE f.depth < ?
				  AND ac.consumer_repo != ''
				  AND ac.consumer_repo != f.from_repo
				  AND ac.consumer_repo != ac.provider_repo
				UNION ALL
				SELECT f.to_repo, ec.consumer_repo, 'event_contract', ec.topic, 1.0, f.depth + 1
				FROM flow f
				JOIN event_contracts ec ON ec.producer_repo = f.to_repo
				WHERE f.depth < ?
				  AND ec.consumer_repo != ''
				  AND ec.consumer_repo != f.from_repo
				  AND ec.consumer_repo != ec.producer_repo
			)
			SELECT DISTINCT from_repo, to_repo, edge_type, detail, confidence FROM flow
		`
	}

	rows, err := d.db.Query(query, trigger, trigger, maxHops, maxHops)
	if err != nil {
		return nil, fmt.Errorf("orgdb: trace flow %q %s: %w", trigger, direction, err)
	}
	defer rows.Close()

	steps := []FlowStep{}
	for rows.Next() {
		var s FlowStep
		if err := rows.Scan(&s.FromRepo, &s.ToRepo, &s.EdgeType, &s.Detail, &s.Confidence); err != nil {
			return nil, fmt.Errorf("orgdb: scan flow step: %w", err)
		}
		steps = append(steps, s)
	}
	return steps, rows.Err()
}

// TeamTopology returns a team's repos and inter-team dependencies.
func (d *DB) TeamTopology(team string) (TeamInfo, error) {
	info := TeamInfo{Team: team}

	// Get team's repos
	rows, err := d.db.Query(
		`SELECT r.name, r.type, r.node_count, r.edge_count
		 FROM repos r
		 LEFT JOIN team_ownership t ON t.repo_name = r.name
		 WHERE COALESCE(NULLIF(r.team, ''), t.team) = ?
		 ORDER BY r.name`,
		team,
	)
	if err != nil {
		return info, fmt.Errorf("orgdb: team topology repos %q: %w", team, err)
	}
	defer rows.Close()

	for rows.Next() {
		var r RepoSummary
		if err := rows.Scan(&r.Name, &r.Type, &r.NodeCount, &r.EdgeCount); err != nil {
			return info, fmt.Errorf("orgdb: scan repo summary: %w", err)
		}
		info.Repos = append(info.Repos, r)
	}
	if err := rows.Err(); err != nil {
		return info, err
	}

	// Get dependent teams via package dependencies
	depRows, err := d.db.Query(`
		SELECT DISTINCT COALESCE(NULLIF(r2.team, ''), t2.team) FROM repo_dependencies rd
		JOIN repos r1 ON rd.repo_id = r1.id
		LEFT JOIN team_ownership t1 ON t1.repo_name = r1.name
		JOIN packages p ON rd.package_id = p.id
		JOIN repos r2 ON p.provider_repo = r2.name
		LEFT JOIN team_ownership t2 ON t2.repo_name = r2.name
		WHERE COALESCE(NULLIF(r1.team, ''), t1.team) = ?
		AND COALESCE(NULLIF(r2.team, ''), t2.team) != ?
		AND COALESCE(NULLIF(r2.team, ''), t2.team) != ''
		ORDER BY COALESCE(NULLIF(r2.team, ''), t2.team)
	`, team, team)
	if err != nil {
		return info, fmt.Errorf("orgdb: team topology deps %q: %w", team, err)
	}
	defer depRows.Close()

	for depRows.Next() {
		var depTeam string
		if err := depRows.Scan(&depTeam); err != nil {
			return info, fmt.Errorf("orgdb: scan dep team: %w", err)
		}
		info.DepTeams = append(info.DepTeams, depTeam)
	}
	if err := depRows.Err(); err != nil {
		return info, err
	}

	// Ensure non-nil slices for consistent behavior
	if info.Repos == nil {
		info.Repos = []RepoSummary{}
	}
	if info.DepTeams == nil {
		info.DepTeams = []string{}
	}

	return info, nil
}

// SearchRepos searches repos by name/team with optional type and team filters.
func (d *DB) SearchRepos(query string, scope string, team string, limit int) ([]RepoSearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := d.db.Query(`
		SELECT r.name, COALESCE(NULLIF(r.team, ''), t.team) AS team, r.type, r.languages, 1.0 as score
		FROM repos r
		LEFT JOIN team_ownership t ON t.repo_name = r.name
		WHERE (r.name LIKE '%' || ? || '%' OR COALESCE(NULLIF(r.team, ''), t.team) LIKE '%' || ? || '%')
		AND (? = '' OR ? = 'all' OR r.type = ?)
		AND (? = '' OR COALESCE(NULLIF(r.team, ''), t.team) = ?)
		ORDER BY r.name
		LIMIT ?
	`, query, query, scope, scope, scope, team, team, limit)
	if err != nil {
		return nil, fmt.Errorf("orgdb: search repos %q: %w", query, err)
	}
	defer rows.Close()

	results := []RepoSearchResult{}
	for rows.Next() {
		var r RepoSearchResult
		var languages *string
		if err := rows.Scan(&r.Name, &r.Team, &r.Type, &languages, &r.Score); err != nil {
			return nil, fmt.Errorf("orgdb: scan search result: %w", err)
		}
		if languages != nil {
			r.Languages = *languages
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
