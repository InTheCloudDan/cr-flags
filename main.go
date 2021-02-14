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

	"github.com/antihax/optional"
	"github.com/google/go-github/github"
	ldapi "github.com/launchdarkly/api-client-go"
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
	event, err := parseEvent(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		fmt.Printf("error parsing GitHub event payload at %q: %v", os.Getenv("GITHUB_EVENT_PATH"), err)
	}
	apiToken := os.Getenv("LAUNCHDARKLY_ACCESS_TOKEN")
	if apiToken == "" {
		fmt.Println("LAUNCHDARKLY_ACCESS_TOKEN is not set.")
		os.Exit(1)
	}

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
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	owner := os.Getenv("GITHUB_REPOSITORY_OWNER")
	repo := strings.Split(os.Getenv("GITHUB_REPOSITORY"), "/")
	prService := client.PullRequests
	issuesService := client.Issues
	rawOpts := github.RawOptions{Type: github.Diff}
	raw, _, err := prService.GetRaw(ctx, owner, repo[1], *event.PullRequest.Number, rawOpts)
	diffRows := strings.Split(raw, "\n")
	// chatbox
	var flagsAdded []string
	var flagsRemoved []string
	for _, row := range diffRows {
		if strings.HasPrefix(row, "+") {
			for _, flag := range flags.Items {
				if strings.Contains(row, flag.Key) {
					flagsAdded = append(flagsAdded, flag.Key)
				}
			}
		} else if strings.HasPrefix(row, "-") {
			for _, flag := range flags.Items {
				if strings.Contains(row, flag.Key) {
					flagsRemoved = append(flagsAdded, flag.Key)
				}
			}
		}
	}
	if err != nil {
		fmt.Println(err)
	}
	for _, flag := range flagsAdded {
		createComment, err := githubComment(flags.Items, flag, ldEnvironment)
		if err != nil {
			fmt.Println(err)
		}
		_, _, err = issuesService.CreateComment(ctx, owner, repo[1], *event.PullRequest.Number, createComment)
		if err != nil {
			fmt.Println(err)
		}
	}
	for _, flag := range flagsRemoved {
		createComment, err := githubComment(flags.Items, flag)
		if err != nil {
			fmt.Println(err)
		}
		_, _, err = issuesService.CreateComment(ctx, owner, repo[1], *event.PullRequest.Number, createComment)
		if err != nil {
			fmt.Println(err)
		}
	}

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

func githubComment(flags []ldapi.FeatureFlag, flag string, environment string) (*github.IssueComment, error) {
	idx, _ := find(flags, flag)
	commentTemplate := map[string]interface{}{
		flag:        flags[idx],
		environment: flags[idx].Environments[environment],
	}
	var commentBody bytes.Buffer
	tmplSetup := `
Flag details: **[{{.Name}}](https://app.launchdarkly.com{{.Environments.dano.Site.Href}})** ` + "`" + `{{.Key}}` + "`" + `
*{{.Description}}*
Tags: {{range $tag := .flag.Tags }}_{{$tag}}_ {{end}}

Default variation: ` + "`" + `{{(index .flag.Variations .environment.Fallthrough_.Variation).Value}}` + "`" + `
Off variation: ` + "`" + `{{(index .flag.Variations .environment.OffVariation).Value}}` + "`" + `
Kind: **{{ .flag.Kind }}**
Temporary: **{{ .flag.Temporary }}**
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
