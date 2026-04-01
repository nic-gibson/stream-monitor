GOOS=windows GOARCH=amd64 go build -ldflags="-w -s -buildid=" -trimpath -o strmon.exe 
GOOS=darwin GOARCH=arm64 go build -ldflags="-w -s -buildid=" -trimpath -o strmon_darwin_arm
GOOS=linux GOARCH=amd64 go build -ldflags="-w -s -buildid=" -trimpath -o strmon_linux_amd


