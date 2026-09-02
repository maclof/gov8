//go:build windows && amd64

package gov8_test

import (
	"fmt"

	gov8 "github.com/maclof/gov8"
)

func Example_executeJavaScript() {
	ownedPlatform := false
	if !gov8.PlatformPresent() {
		if err := gov8.Initialize(); err != nil {
			panic(err)
		}
		ownedPlatform = true
	}
	if ownedPlatform {
		defer func() {
			if err := gov8.Shutdown(); err != nil {
				panic(err)
			}
		}()
	}

	iso, err := gov8.NewIsolate()
	if err != nil {
		panic(err)
	}
	defer iso.Close()
	defer gov8.ReleaseIsolateHostState(iso)

	ctx, err := iso.NewContext()
	if err != nil {
		panic(err)
	}
	defer ctx.Close()

	scope, err := iso.NewScope()
	if err != nil {
		panic(err)
	}
	defer scope.Close()

	script, err := ctx.Compile(scope, `21 * 2`, nil)
	if err != nil {
		panic(err)
	}
	defer script.Close()

	result, err := script.Run(scope, nil)
	if err != nil {
		panic(err)
	}
	n, ok, err := result.IntegerValue(ctx)
	if err != nil || !ok {
		panic("JavaScript result is not an integer")
	}
	fmt.Println(n)
	// Output: 42
}

func Example_goCallback() {
	ownedPlatform := false
	if !gov8.PlatformPresent() {
		if err := gov8.Initialize(); err != nil {
			panic(err)
		}
		ownedPlatform = true
	}
	if ownedPlatform {
		defer func() {
			if err := gov8.Shutdown(); err != nil {
				panic(err)
			}
		}()
	}

	iso, err := gov8.NewIsolate()
	if err != nil {
		panic(err)
	}
	defer iso.Close()
	defer gov8.ReleaseIsolateHostState(iso)

	ctx, err := iso.NewContext()
	if err != nil {
		panic(err)
	}
	defer ctx.Close()

	scope, err := iso.NewScope()
	if err != nil {
		panic(err)
	}
	defer scope.Close()

	add, err := iso.NewFunction(scope, ctx,
		func(cs *gov8.CallbackScope, args gov8.FunctionCallbackArguments, rv gov8.ReturnValue) {
			a, err := args.Get(0)
			if err != nil {
				return
			}
			b, err := args.Get(1)
			if err != nil {
				return
			}
			av, aok, err := cs.IntegerValue(a)
			if err != nil || !aok {
				return
			}
			bv, bok, err := cs.IntegerValue(b)
			if err != nil || !bok {
				return
			}
			_ = rv.SetInt32(int32(av + bv))
		}, nil)
	if err != nil {
		panic(err)
	}

	global, err := ctx.GlobalObject(scope)
	if err != nil {
		panic(err)
	}
	set, err := global.SetByName(scope, ctx, "add", add.Value)
	if err != nil || !set {
		panic("could not define global add")
	}

	script, err := ctx.Compile(scope, `add(20, 22)`, nil)
	if err != nil {
		panic(err)
	}
	defer script.Close()
	result, err := script.Run(scope, nil)
	if err != nil {
		panic(err)
	}
	text, err := result.ToString(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	// Output: 42
}

func Example_callJavaScriptFromGo() {
	ownedPlatform := false
	if !gov8.PlatformPresent() {
		if err := gov8.Initialize(); err != nil {
			panic(err)
		}
		ownedPlatform = true
	}
	if ownedPlatform {
		defer func() {
			if err := gov8.Shutdown(); err != nil {
				panic(err)
			}
		}()
	}

	iso, err := gov8.NewIsolate()
	if err != nil {
		panic(err)
	}
	defer iso.Close()
	defer gov8.ReleaseIsolateHostState(iso)

	ctx, err := iso.NewContext()
	if err != nil {
		panic(err)
	}
	defer ctx.Close()

	scope, err := iso.NewScope()
	if err != nil {
		panic(err)
	}
	defer scope.Close()

	script, err := ctx.Compile(scope,
		`(function (name) { return "Hello, " + name + "!" })`, nil)
	if err != nil {
		panic(err)
	}
	defer script.Close()
	value, err := script.Run(scope, nil)
	if err != nil {
		panic(err)
	}
	fn, ok, err := gov8.AsFunction(value, ctx)
	if err != nil || !ok {
		panic("script did not return a function")
	}
	receiver, err := scope.Undefined()
	if err != nil {
		panic(err)
	}
	name, err := scope.NewString("Go")
	if err != nil {
		panic(err)
	}
	greeting, called, err := fn.Call(scope, receiver, name)
	if err != nil || !called {
		panic("JavaScript function call failed")
	}
	text, err := greeting.ToString(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(text)
	// Output: Hello, Go!
}
