FROM alpine:3.8

RUN apk update
RUN apk add --no-cache git

COPY ld-find-code-refs-github-action /ld-find-code-refs-github-action

LABEL com.github.actions.name="LaunchDarkly Find Flags"
LABEL com.github.actions.description="Flags"
LABEL homepage="https://www.launchdarkly.com"

ENTRYPOINT ["/cr-flags"]
