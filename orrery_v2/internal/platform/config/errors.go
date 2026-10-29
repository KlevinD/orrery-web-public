package config

import "fmt"

type sectionValidationError struct {
	section string
	message string
}

func SectionValidationError(section, message string) error {
	return sectionValidationError{section: section, message: message}
}

func (e sectionValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.section, e.message)
}
