package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"text/template"

	"github.com/InTheCloudDan/cr-flags/ignore"
	"github.com/antihax/optional"
	"github.com/google/go-github/github"
	ldapi "github.com/launchdarkly/api-client-go"
	"github.com/launchdarkly/ld-find-code-refs/coderefs"
	"github.com/launchdarkly/ld-find-code-refs/options"
	"github.com/sourcegraph/go-diff/diff"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"
)

func main() {
	ldProject := os.Getenv("INPUT_PROJECT")
	if ldProject == "" {
		fmt.Println("`project` is required.")
	}
	ldEnvironment := os.Getenv("INPUT_ENVIRONMENT")
	if ldEnvironment == "" {
		fmt.Println("`environment` is required.")
	}
	ldInstance := os.Getenv("INPUT_INSTANCE")
	if ldEnvironment == "" {
		fmt.Println("`instance` is required.")
	}
	owner := os.Getenv("GITHUB_REPOSITORY_OWNER")
	repo := strings.Split(os.Getenv("GITHUB_REPOSITORY"), "/")

	event, err := parseEvent(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		fmt.Printf("error parsing GitHub event payload at %q: %v", os.Getenv("GITHUB_EVENT_PATH"), err)
	}
	apiToken := os.Getenv("LAUNCHDARKLY_ACCESS_TOKEN")
	if apiToken == "" {
		fmt.Println("LAUNCHDARKLY_ACCESS_TOKEN is not set.")
		os.Exit(1)
	}

	// Query for flags
	ldClient, err := newClient(apiToken, "https://app.launchdarkly.com", false)
	if err != nil {
		fmt.Println(err)
	}
	flagOpts := ldapi.GetFeatureFlagsOpts{
		Env:     optional.NewInterface(ldEnvironment),
		Summary: optional.NewBool(false),
	}
	flags, _, err := ldClient.ld.FeatureFlagsApi.GetFeatureFlags(ldClient.ctx, ldProject, &flagOpts)
	if err != nil {
		fmt.Println(err)
	}
	flagKeys := make([]string, 0, len(flags.Items))

	for _, flag := range append(flags.Items) {
		flagKeys = append(flagKeys, flag.Key)
	}

	workspace := os.Getenv("GITHUB_WORKSPACE")
	viper.Set("dir", workspace)
	viper.Set("accessToken", apiToken)

	err = options.InitYAML()
	opts, err := options.GetOptions()
	if err != nil {
		fmt.Println(err)
	}
	if err != nil {
		fmt.Println(err)
	}

	aliases, err := coderefs.GenerateAliases(flagKeys, opts.Aliases, workspace)
	if err != nil {
		fmt.Println(err)
		fmt.Println("failed to create flag key aliases")
	}
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)
	prService := client.PullRequests
	issuesService := client.Issues

	rawOpts := github.RawOptions{Type: github.Diff}
	raw, _, err := prService.GetRaw(ctx, owner, repo[1], *event.PullRequest.Number, rawOpts)
	fmt.Println(raw)
	multiFiles, err := diff.ParseMultiFileDiff([]byte(raw))
	flagsAdded := make(map[string][]string)
	flagsRemoved := make(map[string][]string)
	allIgnores := ignore.NewIgnore(workspace)

	for _, parsedDiff := range multiFiles {
		// If file is being renamed we don't want to check it for flags.
		parsedFileA := strings.SplitN(parsedDiff.OrigName, "/", 2)
		parsedFileB := strings.SplitN(parsedDiff.NewName, "/", 2)
		fmt.Println(parsedFileB)
		fullPathToA := workspace + "/" + parsedFileA[1]
		fullPathToB := workspace + "/" + parsedFileB[1]
		info, err := os.Stat(fullPathToB)
		var isDir bool
		var fileToParse string
		if info == nil {
			isDir = false
			fileToParse = fullPathToA
		} else {
			isDir = info.IsDir()
			fileToParse = fullPathToB

		}
		if err != nil {
			fmt.Println(err)
		}
		if strings.HasPrefix(parsedFileB[1], ".") || allIgnores.Match(fileToParse, isDir) {
			if isDir {
				continue
			}
			continue
		}
		if (parsedFileA[1] != parsedFileB[1]) && !strings.Contains(parsedFileB[1], "dev/null") {
			continue
		}
		for _, raw := range parsedDiff.Hunks {
			diffRows := strings.Split(string(raw.Body), "\n")
			for _, row := range diffRows {
				if strings.HasPrefix(row, "+") {
					for _, flag := range flags.Items {
						if strings.Contains(row, flag.Key) {
							currentKeys := flagsAdded[flag.Key]
							currentKeys = append(currentKeys, "")
							flagsAdded[flag.Key] = currentKeys
						}
						if len(aliases[flag.Key]) > 0 {
							for _, alias := range aliases[flag.Key] {
								if strings.Contains(row, alias) {
									currentKeys := flagsAdded[flag.Key]
									currentKeys = append(currentKeys, alias)
									flagsAdded[flag.Key] = currentKeys
								}
							}
						}
					}
				} else if strings.HasPrefix(row, "-") {
					for _, flag := range flags.Items {
						if strings.Contains(row, flag.Key) {
							currentKeys := flagsRemoved[flag.Key]
							currentKeys = append(currentKeys, "")
							flagsRemoved[flag.Key] = currentKeys
						}
						if len(aliases[flag.Key]) > 0 {
							for _, alias := range aliases[flag.Key] {
								if strings.Contains(row, alias) {
									currentKeys := flagsRemoved[flag.Key]
									currentKeys = append(currentKeys, alias)
									flagsRemoved[flag.Key] = currentKeys
								}
							}
						}
					}
				}
			}
		}

	}
	if err != nil {
		fmt.Println(err)
	}

	comments, _, err := issuesService.ListComments(ctx, owner, repo[1], *event.PullRequest.Number, nil)
	if err != nil {
		fmt.Println(err)
	}
	var existingComment int64
	for _, comment := range comments {
		if strings.Contains(*comment.Body, "LaunchDarkly Flag Details") {
			existingComment = int64(comment.GetID())
		}
	}
	for flag, aliases := range flagsAdded {
		// If flag is in both added and removed then it is being modified
		delete(flagsRemoved, flag)
		createComment, err := githubFlagComment(flags.Items, flag, aliases, "Added/Modified", ldEnvironment, ldInstance)
		if err != nil {
			fmt.Println(err)
		}
		if existingComment > 0 {
			_, _, err = issuesService.EditComment(ctx, owner, repo[1], existingComment, createComment)
		} else {
			_, _, err = issuesService.CreateComment(ctx, owner, repo[1], *event.PullRequest.Number, createComment)
		}
		if err != nil {
			fmt.Println(err)
		}
	}
	for flag, aliases := range flagsRemoved {

		createComment, err := githubFlagComment(flags.Items, flag, aliases, "Removed", ldEnvironment, ldInstance)
		if err != nil {
			fmt.Println(err)
		}
		if existingComment > 0 {
			_, _, err = issuesService.EditComment(ctx, owner, repo[1], existingComment, createComment)
		} else {
			_, _, err = issuesService.CreateComment(ctx, owner, repo[1], *event.PullRequest.Number, createComment)
		}
		if err != nil {
			fmt.Println(err)
		}
	}
	if (len(flagsAdded) == 0 && len(flagsRemoved) == 0) && !(existingComment > 0) {
		createComment := githubNoFlagComment()
		_, _, err = issuesService.CreateComment(ctx, owner, repo[1], *event.PullRequest.Number, createComment)
		if err != nil {
			fmt.Println(err)
		}
	}
}

func remove(s []string, i int) []string {
	s[i] = s[len(s)-1]
	return s[:len(s)-1]
}

func parseEvent(path string) (*github.PullRequestEvent, error) {
	/* #nosec */
	eventJsonFile, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	eventJsonBytes, err := ioutil.ReadAll(eventJsonFile)
	if err != nil {
		return nil, err
	}
	var evt github.PullRequestEvent
	err = json.Unmarshal(eventJsonBytes, &evt)
	if err != nil {
		return nil, err
	}
	return &evt, err
}

type Client struct {
	apiKey  string
	apiHost string
	ld      *ldapi.APIClient
	ctx     context.Context
}

const (
	APIVersion = "20191212"
)

func newClient(token string, apiHost string, oauth bool) (*Client, error) {
	if token == "" {
		return nil, errors.New("token cannot be empty")
	}
	basePath := "https://app.launchdarkly.com/api/v2"
	if apiHost != "" {
		basePath = fmt.Sprintf("%s/api/v2", apiHost)
	}

	cfg := &ldapi.Configuration{
		BasePath:      basePath,
		DefaultHeader: make(map[string]string),
		UserAgent:     fmt.Sprintf("launchdarkly-terraform-provider/0.1.0"),
	}

	cfg.AddDefaultHeader("LD-API-Version", APIVersion)

	ctx := context.WithValue(context.Background(), ldapi.ContextAPIKey, ldapi.APIKey{
		Key: token,
	})
	if oauth {
		ctx = context.WithValue(context.Background(), ldapi.ContextAccessToken, token)
	}

	return &Client{
		apiKey:  token,
		apiHost: apiHost,
		ld:      ldapi.NewAPIClient(cfg),
		ctx:     ctx,
	}, nil
}

func find(slice []ldapi.FeatureFlag, val string) (int, bool) {
	for i, item := range slice {
		if item.Key == val {
			return i, true
		}
	}
	return -1, false
}

type Comment struct {
	Flag        ldapi.FeatureFlag
	Aliases     []string
	ChangeType  string
	Environment ldapi.FeatureFlagConfig
	LDInstance  string
}

func githubFlagComment(flags []ldapi.FeatureFlag, flag string, aliases []string, changeType string, environment string, instance string) (*github.IssueComment, error) {
	idx, _ := find(flags, flag)
	commentTemplate := Comment{
		Flag:        flags[idx],
		Aliases:     aliases,
		ChangeType:  changeType,
		Environment: flags[idx].Environments[environment],
		LDInstance:  instance,
	}
	var commentBody bytes.Buffer
	tmplSetup := `
LaunchDarkly Flag Details **{{ .ChangeType }}**
**[{{.Flag.Name}}]({{.LDInstance}}{{.Environment.Site.Href}})** ` + "`" + `{{.Flag.Key}}` + "`" + `
*{{.Flag.Description}}*
Tags: {{range $tag := .Flag.Tags }}_{{$tag}}_ {{end}}

Default variation: ` + "`" + `{{(index .Flag.Variations .Environment.Fallthrough_.Variation).Value}}` + "`" + `
Off variation: ` + "`" + `{{(index .Flag.Variations .Environment.OffVariation).Value}}` + "`" + `
Kind: **{{ .Flag.Kind }}**
Temporary: **{{ .Flag.Temporary }}**
Aliases: {{ .Aliases }}
`
	tmpl, err := template.New("comment").Parse(tmplSetup)
	if err != nil {
		return nil, err
	}
	err = tmpl.Execute(&commentBody, commentTemplate)
	commentStr := commentBody.String()
	fmt.Println(commentStr)
	comment := github.IssueComment{
		Body: &commentStr,
	}
	return &comment, nil
}

func githubNoFlagComment() *github.IssueComment {
	commentStr := `
LaunchDarkly Flag Details: **No flag references found in PR**`
	comment := github.IssueComment{
		Body: &commentStr,
	}
	return &comment
}
