package main

import (
	"fmt"
	"os"
	"strings"

	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/platform/config"
)

const (
	bootstrapSuperadminPasswordEnv = "ORRERY_BOOTSTRAP_SUPERADMIN_PASSWORD"
)

type systemUserBootstrapPasswords struct {
	Superadmin string
	Production bool
}

func defaultSystemUserBootstrapPasswords() systemUserBootstrapPasswords {
	return systemUserBootstrapPasswords{
		Superadmin: userauth.SuperadminPassword(),
		Production: false,
	}
}

func systemUserBootstrapPasswordsFromConfig(cfg config.Config) (systemUserBootstrapPasswords, error) {
	passwords := systemUserBootstrapPasswords{
		Superadmin: strings.TrimSpace(os.Getenv(bootstrapSuperadminPasswordEnv)),
		Production: isProductionEnvironment(cfg.Environment),
	}
	if !passwords.Production {
		if passwords.Superadmin == "" {
			passwords.Superadmin = userauth.SuperadminPassword()
		}
		return passwords, nil
	}
	if err := requireProductionBootstrapPassword(bootstrapSuperadminPasswordEnv, passwords.Superadmin); err != nil {
		return systemUserBootstrapPasswords{}, err
	}
	return passwords, nil
}

func requireProductionBootstrapPassword(envName string, password string) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("%s is required for the production profile", envName)
	}
	if password == userauth.SuperadminPassword() {
		return fmt.Errorf("%s must not use the development default password", envName)
	}
	if isPlaceholderPassword(password) {
		return fmt.Errorf("%s must be changed from the example placeholder", envName)
	}
	if err := userauth.ValidatePassword(password); err != nil {
		return fmt.Errorf("%s does not meet the password policy: %w", envName, err)
	}
	return nil
}

func isPlaceholderPassword(password string) bool {
	value := strings.ToLower(strings.TrimSpace(password))
	return strings.HasPrefix(value, "change-me") || strings.HasPrefix(value, "changeme")
}

func isProductionEnvironment(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}
