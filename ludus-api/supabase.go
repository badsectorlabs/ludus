package ludusapi

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/supabase-community/auth-go"
	"github.com/supabase-community/auth-go/types"
)

func createUserInSupabase(user UserWithEmailAndPassword, password string) (types.User, error) {
	// For admin actions, you must use your service_role key.
	// The client is initialized with the URL and the key.
	ServerConfiguration.SupabaseURL = strings.TrimSuffix(ServerConfiguration.SupabaseURL, "/")
	var supabaseAuthClient = auth.New("ludus", ServerConfiguration.ServiceRoleKey).
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
		logger.Debug(fmt.Sprintf("Used service key: %s", ServerConfiguration.ServiceRoleKey))
		logger.Debug(fmt.Sprintf("Used supabase URL: %s", ServerConfiguration.SupabaseURL))
		return types.User{}, err
	}

	// 3. User created successfully
	logger.Debug("User creation successful!")
	logger.Debug(fmt.Sprintf("User ID: %s\n", supabaseAdminCreateUserResponse.User.ID))
	logger.Debug(fmt.Sprintf("Email: %s\n", supabaseAdminCreateUserResponse.User.Email))

	return supabaseAdminCreateUserResponse.User, nil
}

func removeUserFromSupabaseByUserID(userID string) error {
	var supabaseAuthClient = auth.New("default", ServerConfiguration.ServiceRoleKey).
		WithCustomAuthURL(ServerConfiguration.SupabaseURL + "/auth/v1").
		WithToken(ServerConfiguration.ServiceRoleKey)

	// Get the user's supabase UUID from the database
	var user UserObject
	db.First(&user, "user_id = ?", userID)
	if user.UserID == "" {
		return errors.New("user not found in database")
	}

	deleteUserParams := types.AdminDeleteUserRequest{
		UserID: user.UUID,
	}

	err := supabaseAuthClient.AdminDeleteUser(deleteUserParams)
	if err != nil {
		return errors.New("error deleting user from supabase: " + err.Error())
	}

	return nil
}

func removeUserFromSupabaseByUUID(userUUID uuid.UUID) error {
	var supabaseAuthClient = auth.New("default", ServerConfiguration.ServiceRoleKey).
		WithCustomAuthURL(ServerConfiguration.SupabaseURL + "/auth/v1").
		WithToken(ServerConfiguration.ServiceRoleKey)

	deleteUserParams := types.AdminDeleteUserRequest{
		UserID: userUUID,
	}

	err := supabaseAuthClient.AdminDeleteUser(deleteUserParams)
	if err != nil {
		return errors.New("error deleting user from supabase: " + err.Error())
	}

	return nil
}
