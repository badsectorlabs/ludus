package logger

import (
	"os"

	"github.com/withmandala/go-log"
)

var Logger *log.Logger

func InitLogger(verbose bool) {
	if verbose {
		Logger = log.New(os.Stderr).WithDebug()
	} else {
		log.ErrorPrefix.File = false
		log.FatalPrefix.File = false
		Logger = log.New(os.Stderr).WithoutTimestamp()
	}

}
