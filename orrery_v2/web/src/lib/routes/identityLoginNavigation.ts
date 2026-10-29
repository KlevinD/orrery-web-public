interface IdentityLoginLocation {
  assign(url: string): void
}

export function navigateToIdentityLogin(url: string, location: IdentityLoginLocation = window.location) {
  location.assign(url)
}

export function navigateToLoginReturnTo(url: string, location: IdentityLoginLocation = window.location) {
  location.assign(url)
}

export function navigateToIdentityLogout(url: string, location: IdentityLoginLocation = window.location) {
  location.assign(url)
}
