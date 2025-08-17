package main

import (
	"github.com/solidDoWant/bridge-operator/tooling/internal/analyzer/ginkgonetns"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(ginkgonetns.Analyzer)
}
