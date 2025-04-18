// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package admin

import (
	"strconv"
	"strings"

	log "unknwon.dev/clog/v2"

	"gogs.io/gogs/internal/conf"
	"gogs.io/gogs/internal/context"
	"gogs.io/gogs/internal/database"
	"gogs.io/gogs/internal/email"
	"gogs.io/gogs/internal/form"
	"gogs.io/gogs/internal/route"
)

const (
	tmplAdminUserList = "admin/user/list"
	tmplAdminUserNew  = "admin/user/new"
	tmplAdminUserEdit = "admin/user/edit"
)

func Users(c *context.Context) {
	c.Data["Title"] = c.Tr("admin.users")
	c.Data["PageIsAdmin"] = true
	c.Data["PageIsAdminUsers"] = true

	route.RenderUserSearch(c, &route.UserSearchOptions{
		Type:     database.UserTypeIndividual,
		Counter:  database.Handle.Users().Count,
		Ranger:   database.Handle.Users().List,
		PageSize: conf.UI.Admin.UserPagingNum,
		OrderBy:  "id ASC",
		TplName:  tmplAdminUserList,
	})
}

func NewUser(c *context.Context) {
	c.Data["Title"] = c.Tr("admin.users.new_account")
	c.Data["PageIsAdmin"] = true
	c.Data["PageIsAdminUsers"] = true

	c.Data["login_type"] = "0-0"

	sources, err := database.Handle.LoginSources().List(c.Req.Context(), database.ListLoginSourceOptions{})
	if err != nil {
		c.Error(err, "list login sources")
		return
	}
	c.Data["Sources"] = sources

	c.Data["CanSendEmail"] = conf.Email.Enabled
	c.Success(tmplAdminUserNew)
}

func NewUserPost(c *context.Context, f form.AdminCrateUser) {
	c.Data["Title"] = c.Tr("admin.users.new_account")
	c.Data["PageIsAdmin"] = true
	c.Data["PageIsAdminUsers"] = true

	sources, err := database.Handle.LoginSources().List(c.Req.Context(), database.ListLoginSourceOptions{})
	if err != nil {
		c.Error(err, "list login sources")
		return
	}
	c.Data["Sources"] = sources

	c.Data["CanSendEmail"] = conf.Email.Enabled

	if c.HasError() {
		c.Success(tmplAdminUserNew)
		return
	}

	createUserOpts := database.CreateUserOptions{
		Password:  f.Password,
		Activated: true,
	}
	if len(f.LoginType) > 0 {
		fields := strings.Split(f.LoginType, "-")
		if len(fields) == 2 {
			createUserOpts.LoginSource, _ = strconv.ParseInt(fields[1], 10, 64)
			createUserOpts.LoginName = f.LoginName
		}
	}

	user, err := database.Handle.Users().Create(c.Req.Context(), f.UserName, f.Email, createUserOpts)
	if err != nil {
		switch {
		case database.IsErrUserAlreadyExist(err):
			c.Data["Err_UserName"] = true
			c.RenderWithErr(c.Tr("form.username_been_taken"), tmplAdminUserNew, &f)
		case database.IsErrEmailAlreadyUsed(err):
			c.Data["Err_Email"] = true
			c.RenderWithErr(c.Tr("form.email_been_used"), tmplAdminUserNew, &f)
		case database.IsErrNameNotAllowed(err):
			c.Data["Err_UserName"] = true
			c.RenderWithErr(c.Tr("user.form.name_not_allowed", err.(database.ErrNameNotAllowed).Value()), tmplAdminUserNew, &f)
		default:
			c.Error(err, "create user")
		}
		return
	}
	log.Trace("Account %q created by admin %q", user.Name, c.User.Name)

	// Send email notification.
	if f.SendNotify && conf.Email.Enabled {
		email.SendRegisterNotifyMail(c.Context, database.NewMailerUser(user))
	}

	c.Flash.Success(c.Tr("admin.users.new_success", user.Name))
	c.Redirect(conf.Server.Subpath + "/admin/users/" + strconv.FormatInt(user.ID, 10))
}

func prepareUserInfo(c *context.Context) *database.User {
	u, err := database.Handle.Users().GetByID(c.Req.Context(), c.ParamsInt64(":userid"))
	if err != nil {
		c.Error(err, "get user by ID")
		return nil
	}
	c.Data["User"] = u

	if u.LoginSource > 0 {
		c.Data["LoginSource"], err = database.Handle.LoginSources().GetByID(c.Req.Context(), u.LoginSource)
		if err != nil {
			c.Error(err, "get login source by ID")
			return nil
		}
	} else {
		c.Data["LoginSource"] = &database.LoginSource{}
	}

	sources, err := database.Handle.LoginSources().List(c.Req.Context(), database.ListLoginSourceOptions{})
	if err != nil {
		c.Error(err, "list login sources")
		return nil
	}
	c.Data["Sources"] = sources

	return u
}

func EditUser(c *context.Context) {
	c.Data["Title"] = c.Tr("admin.users.edit_account")
	c.Data["PageIsAdmin"] = true
	c.Data["PageIsAdminUsers"] = true
	c.Data["EnableLocalPathMigration"] = conf.Repository.EnableLocalPathMigration

	prepareUserInfo(c)
	if c.Written() {
		return
	}

	c.Success(tmplAdminUserEdit)
}

func EditUserPost(c *context.Context, f form.AdminEditUser) {
	c.Data["Title"] = c.Tr("admin.users.edit_account")
	c.Data["PageIsAdmin"] = true
	c.Data["PageIsAdminUsers"] = true
	c.Data["EnableLocalPathMigration"] = conf.Repository.EnableLocalPathMigration

	u := prepareUserInfo(c)
	if c.Written() {
		return
	}

	if c.HasError() {
		c.Success(tmplAdminUserEdit)
		return
	}

	opts := database.UpdateUserOptions{
		LoginName:        &f.LoginName,
		FullName:         &f.FullName,
		Website:          &f.Website,
		Location:         &f.Location,
		MaxRepoCreation:  &f.MaxRepoCreation,
		IsActivated:      &f.Active,
		IsAdmin:          &f.Admin,
		AllowGitHook:     &f.AllowGitHook,
		AllowImportLocal: &f.AllowImportLocal,
		ProhibitLogin:    &f.ProhibitLogin,
	}

	fields := strings.Split(f.LoginType, "-")
	if len(fields) == 2 {
		loginSource, _ := strconv.ParseInt(fields[1], 10, 64)
		if u.LoginSource != loginSource {
			opts.LoginSource = &loginSource
		}
	}

	if f.Password != "" {
		opts.Password = &f.Password
	}

	if u.Email != f.Email {
		opts.Email = &f.Email
	}

	err := database.Handle.Users().Update(c.Req.Context(), u.ID, opts)
	if err != nil {
		if database.IsErrEmailAlreadyUsed(err) {
			c.Data["Err_Email"] = true
			c.RenderWithErr(c.Tr("form.email_been_used"), tmplAdminUserEdit, &f)
		} else {
			c.Error(err, "update user")
		}
		return
	}
	log.Trace("Account updated by admin %q: %s", c.User.Name, u.Name)

	c.Flash.Success(c.Tr("admin.users.update_profile_success"))
	c.Redirect(conf.Server.Subpath + "/admin/users/" + c.Params(":userid"))
}

func DeleteUser(c *context.Context) {
	u, err := database.Handle.Users().GetByID(c.Req.Context(), c.ParamsInt64(":userid"))
	if err != nil {
		c.Error(err, "get user by ID")
		return
	}

	if err = database.Handle.Users().DeleteByID(c.Req.Context(), u.ID, false); err != nil {
		switch {
		case database.IsErrUserOwnRepos(err):
			c.Flash.Error(c.Tr("admin.users.still_own_repo"))
			c.JSONSuccess(map[string]any{
				"redirect": conf.Server.Subpath + "/admin/users/" + c.Params(":userid"),
			})
		case database.IsErrUserHasOrgs(err):
			c.Flash.Error(c.Tr("admin.users.still_has_org"))
			c.JSONSuccess(map[string]any{
				"redirect": conf.Server.Subpath + "/admin/users/" + c.Params(":userid"),
			})
		default:
			c.Error(err, "delete user")
		}
		return
	}
	log.Trace("Account deleted by admin (%s): %s", c.User.Name, u.Name)

	c.Flash.Success(c.Tr("admin.users.deletion_success"))
	c.JSONSuccess(map[string]any{
		"redirect": conf.Server.Subpath + "/admin/users",
	})
}
