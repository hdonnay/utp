// +build debug

package utp

import (
	"fmt"
	"log"
	"os"
)

var l *log.Logger

func init() {
	l = log.New(os.Stderr, "DEBUG", log.Ldate|log.Lmicroseconds|log.Lshortfile)
}

func debug(v ...interface{}) { l.Output(2, fmt.Sprintf(v...)) }
