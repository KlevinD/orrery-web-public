package logger

import "sync"

/**
 * @Author linya.jj
 * @Date 2023/3/22 14:30
 */

type ILogger interface {
	Debugf(format string, args ...interface{})

	Infof(format string, args ...interface{})

	Warningf(format string, args ...interface{})

	Errorf(format string, args ...interface{})

	Fatalf(format string, args ...interface{})
}

var (
	sdkLoggerMu sync.RWMutex
	sdkLogger   ILogger = &doNothingLogger{}
)

func SetLogger(customLogger ILogger) {
	if customLogger == nil {
		customLogger = &doNothingLogger{}
	}
	sdkLoggerMu.Lock()
	sdkLogger = customLogger
	sdkLoggerMu.Unlock()
}

func GetLogger() ILogger {
	sdkLoggerMu.RLock()
	current := sdkLogger
	sdkLoggerMu.RUnlock()
	return current
}

type doNothingLogger struct {
}

func (l *doNothingLogger) Debugf(format string, args ...interface{}) {

}

func (l *doNothingLogger) Infof(format string, args ...interface{}) {

}

func (l *doNothingLogger) Warningf(format string, args ...interface{}) {

}

func (l *doNothingLogger) Errorf(format string, args ...interface{}) {

}

func (l *doNothingLogger) Fatalf(format string, args ...interface{}) {

}
