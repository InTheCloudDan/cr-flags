FROM golang:alpine

RUN apk update
RUN apk add --no-cache git

RUN mkdir /app
WORKDIR /app
COPY . .
ENV GO111MODULE=on
RUN go mod download
RUN go build ./...

LABEL com.github.actions.name="LaunchDarkly Find Flags"
LABEL com.github.actions.description="Flags"
LABEL homepage="https://www.launchdarkly.com"

ENTRYPOINT ["/app/cr-flags"]
