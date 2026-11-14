package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
	"gopkg.in/yaml.v3"
)

// TemplateExportOptions selects the live configuration to turn into a
// registry-compatible template file.
type TemplateExportOptions struct {
	Kind    string // squad | agent | project
	ID      string // live entity id or exact name
	OutDir  string // template repository root
	IDSlug  string // optional metadata.id override
	Squad   string // project only: team squad (id/name); default = unique squad assignee
	Version string // metadata.version (default 0.1.0)
	// WithSkills embeds the files of every referenced skill under skills/<name>/
	// so the template is portable to an installation that lacks them.
	WithSkills bool
	// Force overwrites existing template/skill files and updates the registry.
	Force bool
}

// TemplateExportResult reports what was written.
type TemplateExportResult struct {
	Kind     string   `json:"kind"`
	ID       string   `json:"template_id"`
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Registry string   `json:"registry"`
	Skills   []string `json:"skills,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// TemplateExportService snapshots existing agents/squads/projects into the
// YAML template format consumed by TemplateService + TemplateApplyService.
type TemplateExportService struct {
	st      *store.Store
	agents  *AgentService
	squads  *SquadService
	domains *DomainService
	goals   *GoalService
	scheds  *ScheduleService
	skills  *SkillService
}

func NewTemplateExportService(st *store.Store, bus *events.Bus) *TemplateExportService {
	return &TemplateExportService{
		st:      st,
		agents:  NewAgentService(st, bus),
		squads:  NewSquadService(st, bus),
		domains: NewDomainService(st, bus),
		goals:   NewGoalService(st, bus),
		scheds:  NewScheduleService(st, bus),
		skills:  NewSkillService(st),
	}
}

// Export writes one template file plus its registry entry.
func (s *TemplateExportService) Export(ctx context.Context, opts TemplateExportOptions) (*TemplateExportResult, error) {
	if strings.TrimSpace(opts.OutDir) == "" {
		return nil, fmt.Errorf("export: -out is required")
	}
	if opts.Version == "" {
		opts.Version = "0.1.0"
	}
	res := &TemplateExportResult{Warnings: []string{}}

	var (
		meta TemplateMeta
		raw  []byte
		err  error
		refs []string
	)
	switch strings.TrimSpace(opts.Kind) {
	case "agent", "agent-template":
		meta, raw, refs, err = s.exportAgent(ctx, opts, res)
	case "squad", "squad-template":
		meta, raw, refs, err = s.exportSquad(ctx, opts, res)
	case "project", "project-template":
		meta, raw, refs, err = s.exportProject(ctx, opts, res)
	default:
		return nil, fmt.Errorf("export: kind must be squad, agent or project")
	}
	if err != nil {
		return nil, err
	}
	meta.APIVersion = "agentwork/v1"

	// Validate the generated YAML with the SAME strict parser the apply path
	// uses, before touching the output directory.
	if _, _, err := ParseTemplateForTest(string(raw)); err != nil {
		return nil, fmt.Errorf("export: generated template does not parse: %w", err)
	}

	dir := templateDir(meta.Kind)
	rel := filepath.ToSlash(filepath.Join(dir, meta.ID+".yaml"))
	dst := filepath.Join(opts.OutDir, dir, meta.ID+".yaml")
	if err := writeTemplateFile(dst, raw, opts.Force); err != nil {
		return nil, err
	}
	res.Kind, res.ID, res.Name, res.Path = meta.Kind, meta.ID, meta.Name, dst
	res.Registry = filepath.Join(opts.OutDir, "registry.yaml")
	if err := upsertRegistry(res.Registry, templateRegistryEntry{
		ID: meta.ID, Kind: meta.Kind, Path: rel, Name: meta.Name,
		Description: meta.Description, Version: meta.Version, Tags: meta.Tags,
	}, opts.Force); err != nil {
		return nil, err
	}

	if opts.WithSkills {
		names, err := s.embedSkills(ctx, opts.OutDir, refs, opts.Force, res)
		if err != nil {
			return nil, err
		}
		res.Skills = names
	}
	return res, nil
}

func templateDir(kind string) string {
	switch kind {
	case TemplateKindSquad:
		return "squads"
	case TemplateKindProject:
		return "projects"
	default:
		return "agents"
	}
}

func writeTemplateFile(path string, raw []byte, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("export: %s already exists (use -force to overwrite)", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("export: mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return fmt.Errorf("export: write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("export: finalize: %w", err)
	}
	return nil
}

type templateRegistryEntry struct {
	ID          string   `yaml:"id"`
	Kind        string   `yaml:"kind"`
	Path        string   `yaml:"path"`
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Version     string   `yaml:"version"`
	Tags        []string `yaml:"tags"`
}

func upsertRegistry(path string, entry templateRegistryEntry, force bool) error {
	reg := templateRegistry{}
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, &reg); err != nil {
			return fmt.Errorf("export: parse existing registry: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("export: read registry: %w", err)
	}
	replaced := false
	for i := range reg.Templates {
		if reg.Templates[i].ID == entry.ID {
			if !force {
				return fmt.Errorf("export: registry already has %q (use -force to update)", entry.ID)
			}
			reg.Templates[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		reg.Templates = append(reg.Templates, entry)
	}
	sort.SliceStable(reg.Templates, func(i, j int) bool {
		if reg.Templates[i].Kind != reg.Templates[j].Kind {
			return reg.Templates[i].Kind < reg.Templates[j].Kind
		}
		return reg.Templates[i].ID < reg.Templates[j].ID
	})
	raw, err := yaml.Marshal(&reg)
	if err != nil {
		return fmt.Errorf("export: encode registry: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return fmt.Errorf("export: write registry: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("export: finalize registry: %w", err)
	}
	return nil
}

func (s *TemplateExportService) exportAgent(ctx context.Context, opts TemplateExportOptions, res *TemplateExportResult) (TemplateMeta, []byte, []string, error) {
	a, err := s.findAgent(ctx, opts.ID)
	if err != nil {
		return TemplateMeta{}, nil, nil, err
	}
	if a.Type == "steward" {
		res.Warnings = append(res.Warnings, fmt.Sprintf("agent %q is the built-in steward; templates exporting a steward are installation-specific", a.Name))
	}
	ta, refs, warns, err := s.toTplAgent(ctx, *a)
	if err != nil {
		return TemplateMeta{}, nil, nil, err
	}
	res.Warnings = append(res.Warnings, warns...)
	meta := TemplateMeta{
		Kind: TemplateKindAgent, Version: opts.Version, Tags: []string{}, Icon: "",
		ID:   s.templateID(opts.IDSlug, a.Name, "agent", a.ID),
		Name: a.Name, Description: a.Description,
	}
	spec := agentSpec{Strategy: conflictUpsert, Agent: ta}
	raw, err := marshalTemplate(meta, spec)
	return meta, raw, refs, err
}

func (s *TemplateExportService) exportSquad(ctx context.Context, opts TemplateExportOptions, res *TemplateExportResult) (TemplateMeta, []byte, []string, error) {
	sq, err := s.findSquad(ctx, opts.ID)
	if err != nil {
		return TemplateMeta{}, nil, nil, err
	}
	members, err := s.squads.ListMembers(ctx, sq.ID)
	if err != nil {
		return TemplateMeta{}, nil, nil, err
	}
	leader, err := s.findAgent(ctx, sq.LeaderID)
	if err != nil {
		return TemplateMeta{}, nil, nil, fmt.Errorf("export: squad leader: %w", err)
	}
	taLeader, refs, warns, err := s.toTplAgent(ctx, *leader)
	if err != nil {
		return TemplateMeta{}, nil, nil, err
	}
	res.Warnings = append(res.Warnings, warns...)
	agents := []tplAgent{taLeader}
	seen := map[string]bool{leader.Name: true}
	tplMembers := []tplMember{}
	for _, m := range members {
		if m.MemberType != "agent" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("squad member %q is %s type; only agent members are representable in a squad template", m.MemberID, m.MemberType))
			continue
		}
		if m.MemberID == leader.ID || seen[m.MemberID] {
			continue
		}
		a, err := s.findAgent(ctx, m.MemberID)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("squad member agent %s not found: %v", m.MemberID, err))
			continue
		}
		ta, r, w, err := s.toTplAgent(ctx, *a)
		if err != nil {
			return TemplateMeta{}, nil, nil, err
		}
		res.Warnings = append(res.Warnings, w...)
		refs = append(refs, r...)
		agents = append(agents, ta)
		seen[a.Name] = true
		role := m.Role
		if role == "" {
			role = "member"
		}
		tplMembers = append(tplMembers, tplMember{Name: a.Name, Role: role})
	}
	if instructionsContainMentions(sq.Instructions) {
		res.Warnings = append(res.Warnings, "squad instructions contain hard-coded mention://agent/<id> links; re-link them after applying (new agents get new ids)")
	}
	for _, a := range agents {
		if instructionsContainMentions(a.SystemPrompt) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("agent %q system_prompt contains hard-coded mention://agent/<id> links; re-link after applying", a.Name))
		}
	}
	meta := TemplateMeta{
		Kind: TemplateKindSquad, Version: opts.Version, Tags: []string{}, Icon: "",
		ID:   s.templateID(opts.IDSlug, sq.Name, "squad", sq.ID),
		Name: sq.Name, Description: sq.Description,
	}
	spec := squadSpec{
		Strategy: conflictUpsert,
		Agents:   agents,
		Squad: tplSquad{
			Name: sq.Name, Description: sq.Description, Leader: leader.Name,
			Instructions: sq.Instructions, Members: tplMembers,
		},
	}
	raw, err := marshalTemplate(meta, spec)
	return meta, raw, refs, err
}

func (s *TemplateExportService) exportProject(ctx context.Context, opts TemplateExportOptions, res *TemplateExportResult) (TemplateMeta, []byte, []string, error) {
	d, err := s.findDomain(ctx, opts.ID)
	if err != nil {
		return TemplateMeta{}, nil, nil, err
	}
	goals, err := s.goals.List(ctx)
	if err != nil {
		return TemplateMeta{}, nil, nil, err
	}
	scheds, err := s.scheds.List(ctx)
	if err != nil {
		return TemplateMeta{}, nil, nil, err
	}

	var team *squadSpec
	var teamRefs []string
	squadID := ""
	if opts.Squad != "" {
		sq, err := s.findSquad(ctx, opts.Squad)
		if err != nil {
			return TemplateMeta{}, nil, nil, err
		}
		squadID = sq.ID
	} else {
		ids := map[string]bool{}
		for _, g := range goals {
			if g.DomainID == d.ID && g.AssigneeType == "squad" && g.AssigneeID != "" {
				ids[g.AssigneeID] = true
			}
		}
		for _, sc := range scheds {
			if sc.DomainID == d.ID && sc.AssigneeType == "squad" && sc.AssigneeID != "" {
				ids[sc.AssigneeID] = true
			}
		}
		if len(ids) > 1 {
			return TemplateMeta{}, nil, nil, fmt.Errorf("export: project %q is used by %d squads; pass -squad to choose the team", d.Name, len(ids))
		}
		for id := range ids {
			squadID = id
		}
	}
	if squadID != "" {
		sq, err := s.findSquad(ctx, squadID)
		if err != nil {
			return TemplateMeta{}, nil, nil, err
		}
		var refs []string
		team, refs, err = s.teamSpec(ctx, sq, res)
		if err != nil {
			return TemplateMeta{}, nil, nil, err
		}
		teamRefs = refs
	}

	tplGoals := []tplGoal{}
	for _, g := range goals {
		if g.DomainID != d.ID {
			continue
		}
		switch g.Status {
		case "done", "failed", "cancelled":
			continue
		}
		if g.AssigneeType == "human" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("goal %q is assigned to a human; project templates can only assign agent/squad goals", g.Title))
			continue
		}
		name, ok := s.assigneeName(ctx, g.AssigneeType, g.AssigneeID)
		if !ok {
			res.Warnings = append(res.Warnings, fmt.Sprintf("goal %q assignee %s not found; skipped", g.Title, g.AssigneeID))
			continue
		}
		tplGoals = append(tplGoals, tplGoal{
			Title: g.Title, Description: g.Description,
			Assignee: name, AssigneeType: g.AssigneeType, Start: g.Status == "active",
		})
	}
	tplScheds := []tplSchedule{}
	for _, sc := range scheds {
		if sc.DomainID != d.ID {
			continue
		}
		if sc.BuiltIn {
			res.Warnings = append(res.Warnings, fmt.Sprintf("schedule %q is built-in; skipped", sc.Name))
			continue
		}
		name, ok := s.assigneeName(ctx, sc.AssigneeType, sc.AssigneeID)
		if !ok {
			res.Warnings = append(res.Warnings, fmt.Sprintf("schedule %q assignee %s not found; skipped", sc.Name, sc.AssigneeID))
			continue
		}
		tplScheds = append(tplScheds, tplSchedule{
			Name: sc.Name, Title: sc.TitleTemplate, Description: sc.Description,
			Cron: sc.CronExpression, Assignee: name, AssigneeType: sc.AssigneeType,
		})
	}

	issueAssignee := ""
	if d.IssueAssignee != "" {
		if name, ok := s.assigneeName(ctx, d.IssueAssigneeType, d.IssueAssignee); ok {
			issueAssignee = name
		} else {
			res.Warnings = append(res.Warnings, "domain issue_assignee could not be resolved to a name; left blank")
		}
	}
	meta := TemplateMeta{
		Kind: TemplateKindProject, Version: opts.Version, Tags: []string{}, Icon: "",
		ID:   s.templateID(opts.IDSlug, d.Name, "project", d.ID),
		Name: d.Name,
	}
	if d.PolicyText != "" {
		meta.Description = strings.TrimSpace(strings.SplitN(d.PolicyText, "\n", 2)[0])
	}
	spec := projectSpec{
		Domain: tplDomain{
			Type: d.Type, GitURL: d.GitURL, DefaultBranch: d.DefaultBranch, GitIdentity: d.GitIdentity,
			IssueAssignee: issueAssignee, IssueAssigneeType: d.IssueAssigneeType,
			PolicyText: d.PolicyText,
		},
		Team:      team,
		Goals:     tplGoals,
		Schedules: tplScheds,
	}
	if d.Type != "scratch" && strings.TrimSpace(d.GitURL) == "" {
		res.Warnings = append(res.Warnings, "repo project template has no git_url in the spec; apply requires -git-url (or repo.create) because a repo cannot be inferred")
	}
	raw, err := marshalTemplate(meta, spec)
	return meta, raw, teamRefs, err
}

// teamSpec builds the embedded squadSpec without writing a standalone squad file.
func (s *TemplateExportService) teamSpec(ctx context.Context, sq *Squad, res *TemplateExportResult) (*squadSpec, []string, error) {
	members, err := s.squads.ListMembers(ctx, sq.ID)
	if err != nil {
		return nil, nil, err
	}
	leader, err := s.findAgent(ctx, sq.LeaderID)
	if err != nil {
		return nil, nil, fmt.Errorf("export: squad leader: %w", err)
	}
	taLeader, refs, warns, err := s.toTplAgent(ctx, *leader)
	if err != nil {
		return nil, nil, err
	}
	res.Warnings = append(res.Warnings, warns...)
	agents := []tplAgent{taLeader}
	seen := map[string]bool{leader.Name: true}
	tplMembers := []tplMember{}
	for _, m := range members {
		if m.MemberType != "agent" {
			res.Warnings = append(res.Warnings, fmt.Sprintf("squad member %q is %s type; only agent members are representable in a project team", m.MemberID, m.MemberType))
			continue
		}
		if m.MemberID == leader.ID || seen[m.MemberID] {
			continue
		}
		a, err := s.findAgent(ctx, m.MemberID)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("squad member agent %s not found: %v", m.MemberID, err))
			continue
		}
		ta, r, w, err := s.toTplAgent(ctx, *a)
		if err != nil {
			return nil, nil, err
		}
		res.Warnings = append(res.Warnings, w...)
		refs = append(refs, r...)
		agents = append(agents, ta)
		seen[a.Name] = true
		role := m.Role
		if role == "" {
			role = "member"
		}
		tplMembers = append(tplMembers, tplMember{Name: a.Name, Role: role})
	}
	if instructionsContainMentions(sq.Instructions) {
		res.Warnings = append(res.Warnings, "squad instructions contain hard-coded mention://agent/<id> links; re-link them after applying")
	}
	for _, a := range agents {
		if instructionsContainMentions(a.SystemPrompt) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("agent %q system_prompt contains hard-coded mention://agent/<id> links; re-link after applying", a.Name))
		}
	}
	return &squadSpec{
		Strategy: conflictUpsert,
		Agents:   agents,
		Squad: tplSquad{
			Name: sq.Name, Description: sq.Description, Leader: leader.Name,
			Instructions: sq.Instructions, Members: tplMembers,
		},
	}, refs, nil
}

func (s *TemplateExportService) toTplAgent(ctx context.Context, a Agent) (tplAgent, []string, []string, error) {
	warns := []string{}
	secretKeyRe := regexp.MustCompile(`(?i)(token|secret|passw|api[_-]?key|credential|authorization)`)
	for k := range a.Env {
		if secretKeyRe.MatchString(k) {
			warns = append(warns, fmt.Sprintf("agent %q env %q looks like a secret; template YAML stores it in plaintext", a.Name, k))
		}
	}
	for _, m := range a.McpServers {
		for _, e := range m.Env {
			if secretKeyRe.MatchString(e.Name) {
				warns = append(warns, fmt.Sprintf("agent %q MCP %q env %q looks like a secret; template YAML stores it in plaintext", a.Name, m.Name, e.Name))
			}
		}
		for _, h := range m.Headers {
			if secretKeyRe.MatchString(h.Name) {
				warns = append(warns, fmt.Sprintf("agent %q MCP %q header %q carries credentials; template YAML stores it in plaintext", a.Name, m.Name, h.Name))
			}
		}
	}
	skillNames := []string{}
	refs := []string{}
	for _, id := range a.Skills {
		name, err := s.skillName(ctx, id)
		if err != nil {
			return tplAgent{}, nil, nil, fmt.Errorf("export: agent %q skill %s: %w", a.Name, id, err)
		}
		skillNames = append(skillNames, name)
		refs = append(refs, name)
	}
	mcp := make([]map[string]any, 0, len(a.McpServers))
	for _, m := range a.McpServers {
		b, err := json.Marshal(m)
		if err != nil {
			return tplAgent{}, nil, nil, fmt.Errorf("export: agent %q mcp server: %w", a.Name, err)
		}
		var mm map[string]any
		if err := json.Unmarshal(b, &mm); err != nil {
			return tplAgent{}, nil, nil, fmt.Errorf("export: agent %q mcp server: %w", a.Name, err)
		}
		mcp = append(mcp, mm)
	}
	return tplAgent{
		Name: a.Name, Description: a.Description, SystemPrompt: a.SystemPrompt,
		Skills: skillNames, Runtime: "auto", MaxConcurrent: a.MaxConcurrent,
		Model: a.Model, Env: a.Env, McpServers: mcp,
	}, refs, warns, nil
}

func (s *TemplateExportService) findAgent(ctx context.Context, ref string) (*Agent, error) {
	all, err := s.agents.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == ref || all[i].Name == ref {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("agent %q not found", ref)
}

func (s *TemplateExportService) findSquad(ctx context.Context, ref string) (*Squad, error) {
	all, err := s.squads.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == ref || all[i].Name == ref {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("squad %q not found", ref)
}

func (s *TemplateExportService) findDomain(ctx context.Context, ref string) (*Domain, error) {
	all, err := s.domains.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == ref || all[i].Name == ref {
			return &all[i], nil
		}
	}
	return nil, fmt.Errorf("domain %q not found", ref)
}

func (s *TemplateExportService) skillName(ctx context.Context, id string) (string, error) {
	var name string
	if err := s.st.DB().QueryRowContext(ctx, `SELECT name FROM skill WHERE id=?`, id).Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}

func (s *TemplateExportService) assigneeName(ctx context.Context, typ, id string) (string, bool) {
	if id == "" {
		return "", false
	}
	switch typ {
	case "agent":
		a, err := s.findAgent(ctx, id)
		if err != nil {
			return "", false
		}
		return a.Name, true
	case "squad":
		sq, err := s.findSquad(ctx, id)
		if err != nil {
			return "", false
		}
		return sq.Name, true
	}
	return "", false
}

// embedSkills writes skills/<name>/<path> for every referenced skill. Files
// that already exist are kept unless -force; the returned list is the set of
// embedded skill names.
func (s *TemplateExportService) embedSkills(ctx context.Context, outDir string, names []string, force bool, res *TemplateExportResult) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, name := range names {
		if seen[name] || name == "" {
			continue
		}
		seen[name] = true
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skill %q has an unsafe name; not embedded", name))
			continue
		}
		var id string
		if err := s.st.DB().QueryRowContext(ctx, `SELECT id FROM skill WHERE name=?`, name).Scan(&id); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skill %q is not stored on this daemon; not embedded", name))
			continue
		}
		files, err := s.skills.Files(ctx, id)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skill %q files unavailable: %v", name, err))
			continue
		}
		wrote := false
		for rel, content := range files {
			if rel == "package.zip" || strings.HasPrefix(rel, ".git/") {
				continue
			}
			clean := filepath.ToSlash(filepath.Clean(rel))
			if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
				res.Warnings = append(res.Warnings, fmt.Sprintf("skill %q has unsafe path %q; skipped", name, rel))
				continue
			}
			dst := filepath.Join(outDir, "skills", name, filepath.FromSlash(clean))
			if !force {
				if _, err := os.Stat(dst); err == nil {
					continue
				}
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return nil, fmt.Errorf("export: skill %q: %w", name, err)
			}
			if err := os.WriteFile(dst, []byte(content), 0644); err != nil {
				return nil, fmt.Errorf("export: skill %q: %w", name, err)
			}
			wrote = true
		}
		if wrote {
			out = append(out, name)
		} else {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skill %q had no embeddable files; template references it by name (must exist on the target)", name))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *TemplateExportService) templateID(override, name, kind, entityID string) string {
	if override != "" {
		return override
	}
	slug := templateSlug(name)
	if slug == "" {
		slug = templateSlug(kind + "-" + strings.ToLower(entityID))
		if slug == "" {
			slug = kind + "-template"
		}
	}
	return slug
}

var templateSlugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// templateSlug turns a display name into a metadata.id: lowercase ASCII
// letters/digits and single hyphens, 2..63 chars. A fully non-ASCII name
// yields "" and the caller falls back to the entity id.
func templateSlug(name string) string {
	s := strings.ToLower(name)
	s = templateSlugStrip.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "-")
	}
	if len(s) < 2 {
		return ""
	}
	return s
}

func instructionsContainMentions(s string) bool {
	return strings.Contains(s, "mention://agent/") || strings.Contains(s, "mention://squad/")
}

func marshalTemplate(meta TemplateMeta, spec any) ([]byte, error) {
	doc := struct {
		APIVersion string `yaml:"api_version"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			ID          string   `yaml:"id"`
			Name        string   `yaml:"name"`
			Description string   `yaml:"description"`
			Version     string   `yaml:"version"`
			Tags        []string `yaml:"tags"`
			Icon        string   `yaml:"icon"`
		} `yaml:"metadata"`
		Spec any `yaml:"spec"`
	}{
		APIVersion: "agentwork/v1",
		Kind:       meta.Kind,
		Spec:       spec,
	}
	doc.Metadata.ID = meta.ID
	doc.Metadata.Name = meta.Name
	doc.Metadata.Description = meta.Description
	doc.Metadata.Version = meta.Version
	doc.Metadata.Tags = meta.Tags
	doc.Metadata.Icon = meta.Icon
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("export: encode template: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("export: close encoder: %w", err)
	}
	return []byte(buf.String()), nil
}
