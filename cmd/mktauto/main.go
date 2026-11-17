// mktauto: post to your MKTAuto accounts from a folder on your computer.
package main

import (
	"os"

	"github.com/abdullahalnuman0/mktauto-cli/internal/app"
)

// version is set when releasing: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	os.Exit(app.Run(os.Args[1:], version))
}
