package auth

import "context"

type Repository interface { FindByEmail(context.Context, string) (User, error); Create(context.Context, User) (User, error) }
