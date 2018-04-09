package lib

import (
	"fmt"
	"time"

	jwt "github.com/dgrijalva/jwt-go"
)

type MyCustomClaims struct {
	jwt.StandardClaims
}

func IsVaildJwtToken(tokenString string) bool {
	token, err := jwt.ParseWithClaims(tokenString, &MyCustomClaims{},
		func(token *jwt.Token) (interface{}, error) {
			return []byte("test"), nil
		})

	if claims, ok := token.Claims.(*MyCustomClaims); ok && token.Valid {
		now := time.Now().Unix()
		if claims.StandardClaims.VerifyExpiresAt(now, true) {
			return true
		}
	} else {
		fmt.Println(err)
	}
	return false
}
