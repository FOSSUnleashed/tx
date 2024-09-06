GOROOT	= 
MKSHELL	= xsrc

run: tx
	./tx | tee log.^`{date +%Y-%m-%d.%s}

tx: main.go go.mod
	go build


go.mod:
	echo module tx\n\ngo 1.20 > go.mod
	go get github.com/BurntSushi/toml
	go get github.com/ryanuber/go-glob
	go get github.com/thoj/go-ircevent
