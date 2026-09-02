package auth

type User struct { ID, Email string }
type Session struct { Token string; User User }
type MFAConfig struct { Enabled bool; Secret string }
type LoginRequest struct { Email, Password string }
