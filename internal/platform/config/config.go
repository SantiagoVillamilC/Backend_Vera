package config

import "os"

type Config struct {
	Port, DatabaseURL, JWTSecret, SupabaseURL, SupabaseServiceKey, GroqAPIKey, OpenAIAPIKey, OCRWorkerURL string
}

func Load() Config {
	port := os.Getenv("PORT")
	if port == "" { port = "8080" }
	return Config{Port: port, DatabaseURL: os.Getenv("DATABASE_URL"), JWTSecret: os.Getenv("JWT_SECRET"), SupabaseURL: os.Getenv("SUPABASE_URL"), SupabaseServiceKey: os.Getenv("SUPABASE_SERVICE_KEY"), GroqAPIKey: os.Getenv("GROQ_API_KEY"), OpenAIAPIKey: os.Getenv("OPENAI_API_KEY"), OCRWorkerURL: os.Getenv("OCR_WORKER_URL")}
}
