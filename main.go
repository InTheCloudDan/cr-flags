package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/google/go-github/github"
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
	client := github.NewClient(nil)
	ctx := context.Background()
	owner := os.Getenv("GITHUB_REPOSITORY_OWNER")
	repo := strings.Split(os.Getenv("GITHUB_REPOSITORY"), "/")
	fmt.Println(owner)
	fmt.Println(repo)
	prService := client.PullRequests
	rawOpts := github.RawOptions{Type: github.Diff}
	raw, gh, err := prService.GetRaw(ctx, owner, repo[1], *event.PullRequest.Number, rawOpts)
	fmt.Println(gh)
	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(raw)
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
