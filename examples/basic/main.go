//go:build windows && amd64

package main

import (
	"fmt"
	"log"

	gov8 "github.com/maclof/gov8"
)

func run() error {
	if err := gov8.Initialize(); err != nil {
		return err
	}
	defer gov8.Shutdown()

	iso, err := gov8.NewIsolate()
	if err != nil {
		return err
	}
	defer iso.Close()
	defer gov8.ReleaseIsolateHostState(iso)

	ctx, err := iso.NewContext()
	if err != nil {
		return err
	}
	defer ctx.Close()

	scope, err := iso.NewScope()
	if err != nil {
		return err
	}
	defer scope.Close()

	script, err := ctx.Compile(scope, `21 * 2`, nil)
	if err != nil {
		return err
	}
	defer script.Close()

	result, err := script.Run(scope, nil)
	if err != nil {
		return err
	}
	n, ok, err := result.IntegerValue(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("JavaScript result is not an integer")
	}
	fmt.Println(n) // 42
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
