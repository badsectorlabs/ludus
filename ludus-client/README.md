# Ludus Client

## Building for your current OS/Arch

```
export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual"
```

## Building for all OS/Archs

```
export GIT_COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
# Use the fork that doesn't break the terminal on control+c for Linux and macOS
git clone https://github.com/zimeg/spinner
cd spinner && git checkout unhide-interrupts && cd .. && go mod edit -replace github.com/briandowns/spinner=./spinner
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual" -o ./binaries/ludus-client_linux-amd64
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual" -o ./binaries/ludus-client_linux-arm64
GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual" -o ./binaries/ludus-client_macOS-amd64
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual" -o ./binaries/ludus-client_macOS-arm64
# The forked spinner library doesn't compile for windows, so switch back to the original
go mod edit -dropreplace=github.com/briandowns/spinner
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual" -o ./binaries/ludus-client_windows-amd64.exe
GOOS=windows GOARCH=386 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual" -o ./binaries/ludus-client_windows-386.exe
GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "-s -w -X ludus/cmd.GitCommitHash=${GIT_COMMIT_SHORT_HASH}-manual" -o ./binaries/ludus-client_windows-arm64.exe
# All client binaries will be in the `binaries` folder
```