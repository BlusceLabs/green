package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BlusceLabs/green/internal/redaction"
	"github.com/BlusceLabs/green/internal/skills"
)

type skillListOptions struct {
	json bool
}

func runSkills(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command := "list"
	rest := args
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			if err := writeSkillsHelp(stdout); err != nil {
				return exitCrash
			}
			return exitSuccess
		case "list", "add", "info", "remove", "rm", "install", "search":
			command, rest = args[0], args[1:]
		default:
			// Treat a leading flag (e.g. --json) as belonging to the implicit
			// `list` command so `green skills --json` works like `green plugins`.
			if !strings.HasPrefix(args[0], "-") {
				return writeExecUsageError(stderr, fmt.Sprintf("unknown skills subcommand %q", args[0]))
			}
		}
	}

	switch command {
	case "list":
		options, help, err := parseSkillListArgs(rest)
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		if help {
			if err := writeSkillsListHelp(stdout); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		return runSkillsList(deps.skillsDir(), options, stdout, stderr)
	case "add":
		return runSkillAdd(rest, deps.skillsDir(), stdout, stderr)
	case "info":
		return runSkillInfo(rest, deps.skillsDir(), stdout, stderr)
	case "remove", "rm":
		return runSkillRemove(rest, deps.skillsDir(), stdout, stderr)
	case "install":
		return runSkillInstall(rest, deps.skillsDir(), stdout, stderr)
	case "search":
		return runSkillSearch(rest, stdout, stderr)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown skills subcommand %q", command))
	}
}

func runSkillsList(dir string, options skillListOptions, stdout io.Writer, stderr io.Writer) int {
	discovered, err := skills.List(dir)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	// Surface name collisions that List silently resolved (first directory wins),
	// so a shadowed same-named skill is reported instead of just disappearing.
	// Warnings go to stderr, keeping stdout (including --json) clean.
	if dups, derr := skills.Duplicates(dir); derr == nil {
		for _, dup := range dups {
			fmt.Fprintf(stderr, "warning: duplicate skill %q: using %s, ignoring %s\n",
				redaction.RedactString(dup.Name, redaction.Options{}),
				redaction.RedactString(dup.Winner, redaction.Options{}),
				redaction.RedactString(dup.Loser, redaction.Options{}))
		}
	} else {
		// Don't silently swallow a scan failure: "no warnings" would then be
		// ambiguous (no duplicates vs. detection broke). Surface it on stderr.
		fmt.Fprintf(stderr, "warning: could not check for duplicate skills: %s\n",
			redaction.ErrorMessage(derr, redaction.Options{}))
	}
	if options.json {
		payload := struct {
			Skills []skills.Skill `json:"skills"`
		}{Skills: discovered}
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	output := redaction.RedactString(formatSkillList(discovered, dir), redaction.Options{})
	if _, err := fmt.Fprintln(stdout, output); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func formatSkillList(discovered []skills.Skill, dir string) string {
	if len(discovered) == 0 {
		return fmt.Sprintf("No green skills found in %s.", dir)
	}
	lines := []string{"green Skills:"}
	for _, skill := range discovered {
		line := "  " + skill.Name
		if skill.Description != "" {
			line += " - " + skill.Description
		}
		lines = append(lines, line)
		lines = append(lines, "    "+skill.Path)
	}
	return strings.Join(lines, "\n")
}

func parseSkillListArgs(args []string) (skillListOptions, bool, error) {
	options := skillListOptions{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return options, true, nil
		case "--json":
			options.json = true
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown skills list flag %q", arg)}
		}
	}
	return options, false, nil
}

func writeSkillsHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  green skills <command>

Commands:
  list                 List discovered green skills
  add <git-url|path>   Install a skill (checksum-pinned in skills.lock)
  install <id>         Install a skill from a registry by id (category/slug)
  search <query>       Search a skill registry
  info <name>          Show a skill's frontmatter, source, and pinned hash
  remove <name>        Remove an installed skill and its lockfile entry

Registry:
  Skills are fetched from a registry that serves SKILL.md files at
  <registry>/<id>/SKILL.md (id is category/slug). Set the registry with
  --registry or the GREEN_SKILLS_REGISTRY environment variable.
`)
	return err
}

// defaultSkillsRegistry returns the configured registry base URL, or "" if none
// is set. There is no built-in default: operators point green at their own
// (or a community) registry via --registry or GREEN_SKILLS_REGISTRY.
func defaultSkillsRegistry() string {
	if r := os.Getenv("GREEN_SKILLS_REGISTRY"); r != "" {
		return strings.TrimRight(r, "/")
	}
	return ""
}

type skillRegistryOptions struct {
	registry string
	force    bool
	json     bool
}

func parseSkillRegistryArgs(args []string, label string, requireID bool) (string, skillRegistryOptions, bool, error) {
	options := skillRegistryOptions{}
	id := ""
	for _, arg := range args {
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return "", options, true, nil
		case arg == "--registry" || strings.HasPrefix(arg, "--registry="):
			v, ok := strings.CutPrefix(arg, "--registry=")
			if !ok {
				return "", options, false, execUsageError{"--registry requires a value"}
			}
			options.registry = v
		case arg == "--force" || arg == "-f":
			options.force = true
		case arg == "--json":
			options.json = true
		case strings.HasPrefix(arg, "-"):
			return "", options, false, execUsageError{fmt.Sprintf("unknown %s flag %q", label, arg)}
		default:
			if id != "" {
				return "", options, false, execUsageError{fmt.Sprintf("%s takes a single id", label)}
			}
			id = arg
		}
	}
	if requireID && id == "" {
		return "", options, false, execUsageError{fmt.Sprintf("usage: green skills %s <id> [--registry URL] [--force] [--json]", label)}
	}
	if options.registry == "" {
		options.registry = defaultSkillsRegistry()
	}
	return id, options, false, nil
}

// fetchRegistrySkill downloads the SKILL.md for <id> from <registry> into a
// temp directory and returns that directory (the caller is responsible for
// removing it). A registry serves skills at <registry>/<id>/SKILL.md.
func fetchRegistrySkill(ctx context.Context, registry, id string) (string, error) {
	if registry == "" {
		return "", fmt.Errorf("no skill registry configured (pass --registry or set GREEN_SKILLS_REGISTRY)")
	}
	url := fmt.Sprintf("%s/%s/SKILL.md", registry, strings.Trim(id, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch skill %q: %w", id, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("skill %q not found at registry %s", id, registry)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry returned %s for skill %q", resp.Status, id)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "", fmt.Errorf("registry returned an empty SKILL.md for %q", id)
	}
	tmp, err := os.MkdirTemp("", "green-skill-*")
	if err != nil {
		return "", err
	}
	slug := id
	if i := strings.LastIndex(id, "/"); i >= 0 {
		slug = id[i+1:]
	}
	target := filepath.Join(tmp, slug)
	if err := os.MkdirAll(target, 0o755); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), data, 0o644); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	return tmp, nil
}

func runSkillInstall(args []string, dir string, stdout io.Writer, stderr io.Writer) int {
	id, options, help, err := parseSkillRegistryArgs(args, "install", true)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeSkillInstallHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if dir == "" {
		return writeAppError(stderr, "could not resolve the skills directory", exitCrash)
	}
	// Download into a temp dir, then reuse the same pinned-install path as
	// `skill add` so the result is checksum-recorded in skills.lock.
	tmp, err := fetchRegistrySkill(context.Background(), options.registry, id)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	defer os.RemoveAll(tmp)

	result, err := skills.Install(context.Background(), skills.InstallOptions{
		Source: tmp,
		Dir:    dir,
		Force:  options.force,
	})
	if err != nil {
		if errors.Is(err, skills.ErrNameClash) {
			return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
		}
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(result, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if result.Updated {
		fmt.Fprintf(stdout, "Updated skill %q (was %s).\n", result.Name, shortHash(result.PreviousHash))
	} else {
		fmt.Fprintf(stdout, "Installed skill %q from registry.\n", result.Name)
	}
	fmt.Fprintf(stdout, "  hash: %s\n  path: %s\n", result.Hash, result.Path)
	return exitSuccess
}

type registrySearchEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func runSkillSearch(args []string, stdout io.Writer, stderr io.Writer) int {
	query, options, help, err := parseSkillRegistryArgs(args, "search", false)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeSkillSearchHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if options.registry == "" {
		return writeAppError(stderr, "no skill registry configured (pass --registry or set GREEN_SKILLS_REGISTRY)", exitUsage)
	}
	url := fmt.Sprintf("%s/search?q=%s", options.registry, query)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return writeAppError(stderr, fmt.Sprintf("registry returned %s for search", resp.Status), exitUsage)
	}
	var entries []registrySearchEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(fmt.Errorf("parse registry search response: %w", err), redaction.Options{}), exitCrash)
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "No matching skills found.")
		return exitSuccess
	}
	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(entries, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	lines := []string{"Skills:"}
	for _, e := range entries {
		line := "  " + e.ID
		if e.Name != "" {
			line += " - " + e.Name
		}
		if e.Description != "" {
			line += ": " + e.Description
		}
		lines = append(lines, line)
	}
	fmt.Fprintln(stdout, redaction.RedactString(strings.Join(lines, "\n"), redaction.Options{}))
	return exitSuccess
}

func writeSkillInstallHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  green skills install <id> [flags]

Install a skill from a registry by id (category/slug). The registry must serve
the skill's SKILL.md at <registry>/<id>/SKILL.md.

Flags:
      --registry <url>   Registry base URL (or GREEN_SKILLS_REGISTRY)
      --force, -f        Overwrite an existing install from a different source
      --json             Print the install result as JSON
  -h, --help             Show this help
`)
	return err
}

func writeSkillSearchHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  green skills search <query> [flags]

Search a skill registry for skills matching <query>.

Flags:
      --registry <url>   Registry base URL (or GREEN_SKILLS_REGISTRY)
      --json             Print results as JSON
  -h, --help             Show this help
`)
	return err
}

func writeSkillsListHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  green skills list [flags]

Flags:
      --json    Print discovered skills as JSON
  -h, --help    Show this help
`)
	return err
}
