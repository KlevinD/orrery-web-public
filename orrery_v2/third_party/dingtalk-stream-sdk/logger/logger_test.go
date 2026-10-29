package logger

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConcurrentLoggerReplacementAndReads(t *testing.T) {
	const goroutines = 16
	const iterations = 200
	var wg sync.WaitGroup
	for index := 0; index < goroutines; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				if (index+iteration)%2 == 0 {
					SetLogger(nil)
				} else {
					SetLogger(&doNothingLogger{})
				}
				GetLogger().Infof("logger race check")
			}
		}(index)
	}
	wg.Wait()
}

/**
 * @Author linya.jj
 * @Date 2023/3/22 14:30
 */

func TestSetGetSDKLogger(t *testing.T) {
	assert.NotNil(t, GetLogger())

	stdLogger := NewStdTestLogger()
	SetLogger(stdLogger)
	assert.Equal(t, stdLogger, GetLogger())
}
