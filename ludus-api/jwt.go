package ludusapi

import (
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// This function parses the JWT token and returns the email claim and the subject (unique ID) converted to a uuid
func parseJWTToken(token string, hmacSecret []byte) (email string, userUUID uuid.UUID, err error) {
	// Parse the token and validate the signature
	t, err := jwt.ParseWithClaims(token, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return hmacSecret, nil
	})

	// Check if the token is valid
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("error validating token: %v", err)
	} else if claims, ok := t.Claims.(*Claims); ok {
		// Convert the Subject claim to UUID
		subjectUUID, err := uuid.Parse(claims.Subject)
		if err != nil {
			return "", uuid.Nil, fmt.Errorf("error parsing subject as UUID: %v", err)
		}
		return claims.Email, subjectUUID, nil
	}

	return "", uuid.Nil, fmt.Errorf("error parsing token claims")
}
