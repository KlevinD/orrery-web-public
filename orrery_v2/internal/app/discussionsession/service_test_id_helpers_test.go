package discussionsession

import (
	"errors"
	"sync"
	"time"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

type fixedIDs struct {
	mu  sync.Mutex
	ids []string
}

func fixedIDGen(ids ...string) *fixedIDs {
	return &fixedIDs{ids: ids}
}

func (ids *fixedIDs) NewIDContext() (string, error) {
	if ids == nil {
		return "", errors.New("no ids left")
	}
	ids.mu.Lock()
	defer ids.mu.Unlock()
	if len(ids.ids) == 0 {
		return "", errors.New("no ids left")
	}
	id := ids.ids[0]
	ids.ids = ids.ids[1:]
	return id, nil
}
