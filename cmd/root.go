package cmd

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"

	"github.com/gilramir/difftree/difftreelib"
)

type Application struct {
	checkHashes     bool
	logfileName     string
	firstDirectory  string
	secondDirectory string
	ignoreFiles     []string
}

func (self *Application) Run() {

	flag.BoolVar(&self.checkHashes, "check-hashes", false, "Check sha1 of files")
	flag.StringVar(&self.logfileName, "log-file", "", "Where to log")

	flag.Parse()

	if flag.NArg() != 2 {
		fmt.Println("Must give 2 dirs")
		os.Exit(1)
	}
	self.firstDirectory = flag.Arg(0)
	self.secondDirectory = flag.Arg(1)

	setLogger(self.logfileName)

	var engine difftreelib.ComparisonEngine
	var options difftreelib.DifftreeOptions

	options.CheckHashes = self.checkHashes

	/*
		options.IgnoreFiles = make(map[string]bool)
		for _, name := range ignoreFiles {
			options.IgnoreFiles[name] = true
		}
	*/

	err := engine.Compare(self.firstDirectory, self.secondDirectory, &options)
	if err != nil {
		fmt.Printf("Error: %q", err)
		os.Exit(1)
	}

	engine.Summarize()
}

func setLogger(logfileName string) {
	switch logfileName {
	case "":
		log.SetOutput(ioutil.Discard)
		return
	case "-":
		log.SetOutput(os.Stderr)
	default:
		fh, err := os.Create(logfileName)
		if err != nil {
			log.Fatalf("Cannot create %s for logging: %s", logfileName, err)
		}
		log.SetOutput(fh)
	}
	log.SetFlags(log.Ldate | log.Lmicroseconds | log.Lshortfile)
}
