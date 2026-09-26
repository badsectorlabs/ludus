package ludusapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode"
	"unicode/utf8"

	"ludusapi/pveclient"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/sys/unix"
)

var errPasswordRotationPending = errors.New("Password rotation is pending verification on Proxmox; encrypted recovery data is retained. Retry later or contact an administrator")

type passwordRotation struct {
	UserID      string `db:"user_id"`
	Endpoint    string `db:"endpoint"`
	ProxmoxUser string `db:"proxmox_user"`
	OldPassword string `db:"old_password"`
	NewPassword string `db:"new_password"`
	Submitted   bool   `db:"submitted"`
}

type passwordRotator struct {
	app    core.App
	client func() (*pveclient.Client, error)
}

// User records are also saved by activity tracking, API-key rotation, and range
// management. An unrelated save from an older request must not restore the old
// password or API key after a concurrent credential change.
func preserveConcurrentUserCredentials(app core.App) {
	app.OnRecordUpdate("users").BindFunc(func(e *core.RecordEvent) error {
		e.Record.IgnoreUnchangedFields(true)
		return e.Next()
	})
}

func validateRotationPassword(password string) error {
	n := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || n < 8 || n > 64 || len(password) > 72 {
		return errors.New("password must contain 8–64 characters and at most 72 UTF-8 bytes")
	}
	for _, r := range password {
		if unicode.IsControl(r) {
			return errors.New("password must not contain control characters")
		}
	}
	return nil
}

// flock is shared by the regular and admin API processes and released by the
// kernel on a crash. Never unlink these files: waiters must lock the same inode.
// Match the database owner's UID/GID so a lock created by root remains usable
// by the unprivileged service, without making the database directory public.
func lockUserCredentials(ctx context.Context, app core.App, userID string) (func(), error) {
	if userID == "" || filepath.Base(userID) != userID {
		return nil, errors.New("invalid user record ID")
	}
	var owner unix.Stat_t
	if err := unix.Stat(filepath.Join(app.DataDir(), "data.db"), &owner); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(app.DataDir(), ".password-"+userID+".lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if os.Geteuid() == 0 {
		if err := unix.Fchown(fd, int(owner.Uid), int(owner.Gid)); err != nil {
			unix.Close(fd)
			return nil, err
		}
	}
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			unix.Close(fd)
			return nil, err
		}
		select {
		case <-ctx.Done():
			unix.Close(fd)
			return nil, errors.New("another password operation is in progress; retry later")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (r passwordRotator) pending(userID string) (*passwordRotation, error) {
	var pending passwordRotation
	err := r.app.DB().NewQuery("SELECT * FROM _ludus_password_rotations WHERE user_id = {:id}").Bind(dbx.Params{"id": userID}).One(&pending)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &pending, err
}

func deletePasswordRotation(app core.App, userID string) error {
	_, err := app.DB().NewQuery("DELETE FROM _ludus_password_rotations WHERE user_id = {:id}").Bind(dbx.Params{"id": userID}).Execute()
	return err
}

func rotationMatchesUser(pending *passwordRotation, user *core.Record) bool {
	return user.GetString("proxmoxUsername")+"@"+user.GetString("proxmoxRealm") == pending.ProxmoxUser && user.GetString("proxmoxPassword") == pending.OldPassword
}

// Finalize both local credentials and remove the journal in one transaction.
// A failed commit leaves the encrypted journal available after process restart.
func (r passwordRotator) finish(pending *passwordRotation, password string) error {
	err := r.app.RunInTransaction(func(tx core.App) error {
		user, err := tx.FindRecordById("users", pending.UserID)
		if err != nil {
			return err
		}
		if !rotationMatchesUser(pending, user) {
			return errors.New("credentials changed outside the rotation")
		}
		user.SetPassword(password)
		user.Set("proxmoxPassword", pending.NewPassword)
		if err := tx.Save(user); err != nil {
			return err
		}
		return deletePasswordRotation(tx, pending.UserID)
	})
	if err != nil {
		return errPasswordRotationPending
	}
	return nil
}

// recover must hold the user lock. Once a request may have reached Proxmox,
// only successful authentication with the new password proves completion.
// The old password still working does not prove that a timed-out request won't
// finish later. Keep uncertain operations pending rather than replaying them.
func (r passwordRotator) recover(ctx context.Context, userID string) error {
	pending, err := r.pending(userID)
	if err != nil || pending == nil {
		return err
	}
	if !pending.Submitted {
		return deletePasswordRotation(r.app, userID)
	}
	user, err := r.app.FindRecordById("users", userID)
	if err != nil || !rotationMatchesUser(pending, user) {
		return errPasswordRotationPending
	}
	password, err := DecryptStringFromDatabase(pending.NewPassword)
	if err != nil {
		return errPasswordRotationPending
	}
	client, err := r.client()
	if err != nil {
		return errPasswordRotationPending
	}
	if _, err = client.AuthenticatePassword(ctx, pending.Endpoint, pending.ProxmoxUser, password); err != nil {
		return errPasswordRotationPending
	}
	return r.finish(pending, password)
}

func (r passwordRotator) credentials(ctx context.Context, userID string) (*core.Record, error) {
	unlock, err := lockUserCredentials(ctx, r.app, userID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := r.recover(ctx, userID); err != nil {
		return nil, err
	}
	return r.app.FindRecordById("users", userID)
}

func (r passwordRotator) rotate(ctx context.Context, userID, password string) error {
	if err := validateRotationPassword(password); err != nil {
		return err
	}
	unlock, err := lockUserCredentials(ctx, r.app, userID)
	if err != nil {
		return err
	}
	defer unlock()
	if err := r.recover(ctx, userID); err != nil {
		return err
	}
	user, err := r.app.FindRecordById("users", userID)
	if err != nil {
		return errors.New("cannot load the user for password rotation")
	}
	if user.GetString("userID") == "ROOT" {
		return errors.New("You cannot update the password for the root user")
	}
	// Check PocketBase's current password policy before any external change.
	candidate := user.Clone()
	candidate.SetPassword(password)
	if err := r.app.ValidateWithContext(ctx, candidate); err != nil {
		return errors.New("password does not satisfy the Ludus user validation rules")
	}
	oldPassword, err := DecryptStringFromDatabase(user.GetString("proxmoxPassword"))
	if err != nil {
		return errors.New("cannot decrypt the stored Proxmox password")
	}
	client, err := r.client()
	if err != nil {
		return errors.New("cannot connect to Proxmox for password rotation")
	}
	endpoint, err := client.PasswordEndpoint(ctx, user.GetString("proxmoxRealm"))
	if err != nil {
		return err
	}
	proxmoxUser := user.GetString("proxmoxUsername") + "@" + user.GetString("proxmoxRealm")
	session, err := client.AuthenticatePassword(ctx, endpoint, proxmoxUser, oldPassword)
	if err != nil {
		return err
	}
	if password == oldPassword {
		// A repeated successful request must not start another external mutation.
		// In particular, a lost response to a no-op PUT could otherwise appear
		// verified while that request was still able to overwrite a later change.
		if user.ValidatePassword(password) {
			return nil
		}
		pending := &passwordRotation{UserID: userID, ProxmoxUser: proxmoxUser,
			OldPassword: user.GetString("proxmoxPassword"), NewPassword: user.GetString("proxmoxPassword")}
		if err := r.finish(pending, password); err != nil {
			return errors.New("cannot synchronize the Ludus password")
		}
		return nil
	}
	encrypted, err := EncryptStringForDatabase(password)
	if err != nil {
		return errors.New("cannot encrypt the new Proxmox password")
	}
	pending := &passwordRotation{UserID: userID, Endpoint: endpoint, ProxmoxUser: proxmoxUser,
		OldPassword: user.GetString("proxmoxPassword"), NewPassword: encrypted}
	_, err = r.app.DB().NewQuery(`INSERT INTO _ludus_password_rotations
		(user_id, endpoint, proxmox_user, old_password, new_password)
		VALUES ({:id}, {:endpoint}, {:user}, {:old}, {:new})`).Bind(dbx.Params{
		"id": userID, "endpoint": endpoint, "user": proxmoxUser, "old": pending.OldPassword, "new": encrypted,
	}).Execute()
	if err != nil {
		return errors.New("cannot save password recovery data; no password was changed")
	}
	// Commit intent before the network call, so a crash cannot hide an in-flight
	// mutation. Context cancellation after this point must retain recovery data.
	_, err = r.app.DB().NewQuery("UPDATE _ludus_password_rotations SET submitted = 1 WHERE user_id = {:id}").Bind(dbx.Params{"id": userID}).Execute()
	if err != nil {
		return errors.New("cannot record password change intent; no password was changed")
	}
	err = session.ChangePassword(ctx, password)
	if errors.Is(err, pveclient.ErrPasswordRejected) {
		if deletePasswordRotation(r.app, userID) != nil {
			return errPasswordRotationPending
		}
		return err
	}
	// This also reconciles a lost response after Proxmox applied the change.
	// If verification or the local transaction fails, the journal survives.
	return r.recover(ctx, userID)
}

func recoverPendingPasswords(app core.App) {
	var pending []passwordRotation
	if err := app.DB().NewQuery("SELECT user_id FROM _ludus_password_rotations").All(&pending); err != nil {
		app.Logger().Error("Cannot load pending password rotations")
		return
	}
	r := passwordRotator{app: app, client: GetRootPVEClient}
	for _, item := range pending {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := r.credentials(ctx, item.UserID)
		cancel()
		if err != nil {
			app.Logger().Warn(fmt.Sprintf("Password rotation for user record %s still requires recovery", item.UserID))
		}
	}
}
