package ludusapi

import (
	"fmt"
	"log"

	"github.com/supabase-community/auth-go"
	"github.com/supabase-community/auth-go/types"
)

func createUserInSupabase(user UserWithEmailAndPassword, password string) (types.User, error) {
	// For admin actions, you must use your service_role key.
	// The client is initialized with the URL and the key.
	var supabaseAuthClient = auth.New("default", ServerConfiguration.ServiceRoleKey).
		WithCustomAuthURL(ServerConfiguration.SupabaseURL + "/auth/v1").
		WithToken(ServerConfiguration.ServiceRoleKey)

	email := user.Email
	if email == "" {
		email = user.ProxmoxUsername + "@ludus.localhost" // This will be used for all the users who are migrated from 1.x
	}

	log.Printf("Creating user %s with email %s", user.UserID, email)

	params := types.AdminCreateUserRequest{
		Email:        email,
		Password:     &password,
		EmailConfirm: true, // Automatically confirm the user's email
	}

	supabaseAdminCreateUserResponse, err := supabaseAuthClient.AdminCreateUser(params)
	if err != nil {
		log.Printf("Error creating user: %v", err)
		return types.User{}, err
	}

	// 3. User created successfully
	fmt.Println("User creation successful!")
	fmt.Printf("User ID: %s\n", supabaseAdminCreateUserResponse.User.ID)
	fmt.Printf("Email: %s\n", supabaseAdminCreateUserResponse.User.Email)

	return supabaseAdminCreateUserResponse.User, nil
}
