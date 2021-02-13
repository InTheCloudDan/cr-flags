package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"text/template"

	"github.com/google/go-github/github"
	ldapi "github.com/launchdarkly/api-client-go"
	"golang.org/x/oauth2"
)

func main() {
	fmt.Println("Output Stuff")
	dir := os.Getenv("GITHUB_WORKSPACE")
	fmt.Println(dir)

	event, err := parseEvent(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		fmt.Printf("error parsing GitHub event payload at %q: %v", os.Getenv("GITHUB_EVENT_PATH"), err)
	}
	//fmt.Println(event)
	apiToken := os.Getenv("LAUNCHDARKLY_ACCESS_TOKEN")
	if apiToken == "" {
		fmt.Println("LAUNCHDARKLY_ACCESS_TOKEN is not set.")
		os.Exit(1)
	}

	ldClient, err := newClient(apiToken, "https://app.launchdarkly.com", false)
	if err != nil {
		fmt.Println(err)
	}
	projectKey := "support-service"
	flags, _, err := ldClient.ld.FeatureFlagsApi.GetFeatureFlags(ldClient.ctx, projectKey, nil)
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
	fmt.Println(owner)
	fmt.Println(repo)
	prService := client.PullRequests
	issuesService := client.Issues
	rawOpts := github.RawOptions{Type: github.Diff}
	raw, _, err := prService.GetRaw(ctx, owner, repo[1], *event.PullRequest.Number, rawOpts)
	diffRows := strings.Split(raw, "\n")
	// chatbox
	var flagsAdded []string
	for _, row := range diffRows {
		if strings.HasPrefix(row, "+") {
			for _, flag := range flags.Items {
				if strings.Contains(row, flag.Key) {
					flagsAdded = append(flagsAdded, flag.Key)
					fmt.Println("FLAG FOUND")
				}
			}
		} //else if strings.HasPrefix(row, "-") {
		//}
	}
	//fmt.Println(gh)
	if err != nil {
		fmt.Println(err)
	}
	//fmt.Println(raw)

	for _, flag := range flagsAdded {
		idx := sort.Search(len(flags.Items), func(i int) bool {
			return flags.Items[i].Key >= flag
		})
		fmt.Println(idx)
		if flags.Items[idx].Key != flag {
			continue
		}
		var commentBody bytes.Buffer
		tmplSetup := `
Name: **{{.Name}}**
Key: {{.Key}}
{{.Description}}
Tags: *{{.Tags}}
`
		tmpl, err := template.New("comment").Parse(tmplSetup)
		err = tmpl.Execute(&commentBody, flags.Items[idx])
		commentStr := commentBody.String()
		fmt.Println(commentStr)
		comment := github.IssueComment{
			Body: &commentStr,
		}
		ghComment, ghResp, err := issuesService.CreateComment(ctx, owner, repo[1], *event.PullRequest.Number, &comment)
		fmt.Println(ghResp)
		if err != nil {
			fmt.Println(err)
		}
		fmt.Println(ghComment)
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
	//fmt.Println(string(eventJsonBytes))
	var evt github.PullRequestEvent
	err = json.Unmarshal(eventJsonBytes, &evt)
	if err != nil {
		return nil, err
	}
	//fmt.Println(evt)
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
