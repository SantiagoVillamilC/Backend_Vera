package auth

// User es la identidad mínima que devuelve el service.
type User struct {
	ID    string
	Email string
}

// Session es la sesión emitida al autenticar, con su token y su usuario.
type Session struct {
	Token string
	User  User
}

// MFAConfig describe el estado de MFA de un usuario.
type MFAConfig struct {
	Enabled bool
	Secret  string
}

// LoginRequest es el cuerpo de /login y /register.
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}
