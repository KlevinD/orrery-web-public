package main

import "orrery_v2/internal/app/userauth"

func bootstrapAdminScopes() []string {
	return userauth.SuperadminScopes()
}
