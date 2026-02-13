package log

import (
	"fmt"
	"os"
)

var IsVerbose bool

func Info(s string, args ...any) {
	if len(args) > 0 {
		fmt.Printf(s, args...)
	} else {
		fmt.Println(s)
	}
}

func Error(s string, args ...any) {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, s, args...)
	} else {
		fmt.Fprintln(os.Stderr, s)
	}
}

func Verbose(s string, args ...any) {
	if IsVerbose {
		if len(args) > 0 {
			fmt.Printf(s, args...)
		} else {
			fmt.Println(s)
		}
	}
}
