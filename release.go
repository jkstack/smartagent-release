package main

import (
	"fmt"
	"os"
)

func main() {
	_, ok := os.LookupEnv("GITHUB_TOKEN")
	if !ok {
		fmt.Println("Missing GITHUB_TOKEN env")
		os.Exit(1)
	}
	fmt.Println(os.Environ())
}
