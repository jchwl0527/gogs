// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"xorm.io/builder"
	"xorm.io/xorm"

	"gogs.io/gogs/internal/errutil"
	"gogs.io/gogs/internal/repoutil"
	"gogs.io/gogs/internal/userutil"
)

var ErrOrgNotExist = errors.New("Organization does not exist")

// IsOwnedBy returns true if given user is in the owner team.
func (org *User) IsOwnedBy(userID int64) bool {
	return IsOrganizationOwner(org.ID, userID)
}

// IsOrgMember returns true if given user is member of organization.
func (org *User) IsOrgMember(uid int64) bool {
	return org.IsOrganization() && IsOrganizationMember(org.ID, uid)
}

func (org *User) getTeam(e Engine, name string) (*Team, error) {
	return getTeamOfOrgByName(e, org.ID, name)
}

// GetTeamOfOrgByName returns named team of organization.
func (org *User) GetTeam(name string) (*Team, error) {
	return org.getTeam(x, name)
}

func (org *User) getOwnerTeam(e Engine) (*Team, error) {
	return org.getTeam(e, ownerTeamName)
}

// GetOwnerTeam returns owner team of organization.
func (org *User) GetOwnerTeam() (*Team, error) {
	return org.getOwnerTeam(x)
}

func (org *User) getTeams(e Engine) (err error) {
	org.Teams, err = getTeamsByOrgID(e, org.ID)
	return err
}

// GetTeams returns all teams that belong to organization.
func (org *User) GetTeams() error {
	return org.getTeams(x)
}

// TeamsHaveAccessToRepo returns all teams that have given access level to the repository.
func (org *User) TeamsHaveAccessToRepo(repoID int64, mode AccessMode) ([]*Team, error) {
	return GetTeamsHaveAccessToRepo(org.ID, repoID, mode)
}

// GetMembers returns all members of organization.
func (org *User) GetMembers(limit int) error {
	ous, err := GetOrgUsersByOrgID(org.ID, limit)
	if err != nil {
		return err
	}

	org.Members = make([]*User, len(ous))
	for i, ou := range ous {
		org.Members[i], err = Handle.Users().GetByID(context.TODO(), ou.UID)
		if err != nil {
			return err
		}
	}
	return nil
}

// AddMember adds new member to organization.
func (org *User) AddMember(uid int64) error {
	return AddOrgUser(org.ID, uid)
}

// RemoveMember removes member from organization.
func (org *User) RemoveMember(uid int64) error {
	return RemoveOrgUser(org.ID, uid)
}

func (org *User) removeOrgRepo(e Engine, repoID int64) error {
	return removeOrgRepo(e, org.ID, repoID)
}

// RemoveOrgRepo removes all team-repository relations of organization.
func (org *User) RemoveOrgRepo(repoID int64) error {
	return org.removeOrgRepo(x, repoID)
}

// CreateOrganization creates record of a new organization.
func CreateOrganization(org, owner *User) (err error) {
	if err = isUsernameAllowed(org.Name); err != nil {
		return err
	}

	if Handle.Users().IsUsernameUsed(context.TODO(), org.Name, 0) {
		return ErrUserAlreadyExist{
			args: errutil.Args{
				"name": org.Name,
			},
		}
	}

	org.LowerName = strings.ToLower(org.Name)
	if org.Rands, err = userutil.RandomSalt(); err != nil {
		return err
	}
	if org.Salt, err = userutil.RandomSalt(); err != nil {
		return err
	}
	org.UseCustomAvatar = true
	org.MaxRepoCreation = -1
	org.NumTeams = 1
	org.NumMembers = 1

	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}

	if _, err = sess.Insert(org); err != nil {
		return fmt.Errorf("insert organization: %v", err)
	}
	_ = userutil.GenerateRandomAvatar(org.ID, org.Name, org.Email)

	// Add initial creator to organization and owner team.
	if _, err = sess.Insert(&OrgUser{
		UID:      owner.ID,
		OrgID:    org.ID,
		IsOwner:  true,
		NumTeams: 1,
	}); err != nil {
		return fmt.Errorf("insert org-user relation: %v", err)
	}

	// Create default owner team.
	t := &Team{
		OrgID:      org.ID,
		LowerName:  strings.ToLower(ownerTeamName),
		Name:       ownerTeamName,
		Authorize:  AccessModeOwner,
		NumMembers: 1,
	}
	if _, err = sess.Insert(t); err != nil {
		return fmt.Errorf("insert owner team: %v", err)
	}

	if _, err = sess.Insert(&TeamUser{
		UID:    owner.ID,
		OrgID:  org.ID,
		TeamID: t.ID,
	}); err != nil {
		return fmt.Errorf("insert team-user relation: %v", err)
	}

	if err = os.MkdirAll(repoutil.UserPath(org.Name), os.ModePerm); err != nil {
		return fmt.Errorf("create directory: %v", err)
	}

	return sess.Commit()
}

// GetOrgByName returns organization by given name.
func GetOrgByName(name string) (*User, error) {
	if name == "" {
		return nil, ErrOrgNotExist
	}
	u := &User{
		LowerName: strings.ToLower(name),
		Type:      UserTypeOrganization,
	}
	has, err := x.Get(u)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrOrgNotExist
	}
	return u, nil
}

// CountOrganizations returns number of organizations.
func CountOrganizations() int64 {
	count, _ := x.Where("type=1").Count(new(User))
	return count
}

// Organizations returns number of organizations in given page.
func Organizations(page, pageSize int) ([]*User, error) {
	orgs := make([]*User, 0, pageSize)
	return orgs, x.Limit(pageSize, (page-1)*pageSize).Where("type=1").Asc("id").Find(&orgs)
}

// deleteBeans deletes all given beans, beans should contain delete conditions.
func deleteBeans(e Engine, beans ...any) (err error) {
	for i := range beans {
		if _, err = e.Delete(beans[i]); err != nil {
			return err
		}
	}
	return nil
}

// DeleteOrganization completely and permanently deletes everything of organization.
func DeleteOrganization(org *User) error {
	err := Handle.Users().DeleteByID(context.TODO(), org.ID, false)
	if err != nil {
		return err
	}

	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}

	if err = deleteBeans(sess,
		&Team{OrgID: org.ID},
		&OrgUser{OrgID: org.ID},
		&TeamUser{OrgID: org.ID},
	); err != nil {
		return fmt.Errorf("deleteBeans: %v", err)
	}
	return sess.Commit()
}

// ________                ____ ___
// \_____  \_______  ____ |    |   \______ ___________
//  /   |   \_  __ \/ ___\|    |   /  ___// __ \_  __ \
// /    |    \  | \/ /_/  >    |  /\___ \\  ___/|  | \/
// \_______  /__|  \___  /|______//____  >\___  >__|
//         \/     /_____/              \/     \/

// OrgUser represents relations of organizations and their members.
type OrgUser struct {
	ID       int64 `gorm:"primaryKey"`
	UID      int64 `xorm:"uid INDEX UNIQUE(s)" gorm:"column:uid;uniqueIndex:org_user_user_org_unique;index;not null"`
	OrgID    int64 `xorm:"INDEX UNIQUE(s)" gorm:"uniqueIndex:org_user_user_org_unique;index;not null"`
	IsPublic bool  `gorm:"not null;default:FALSE"`
	IsOwner  bool  `gorm:"not null;default:FALSE"`
	NumTeams int   `gorm:"not null;default:0"`
}

// IsOrganizationOwner returns true if given user is in the owner team.
func IsOrganizationOwner(orgID, userID int64) bool {
	has, _ := x.Where("is_owner = ?", true).And("uid = ?", userID).And("org_id = ?", orgID).Get(new(OrgUser))
	return has
}

// IsOrganizationMember returns true if given user is member of organization.
func IsOrganizationMember(orgID, uid int64) bool {
	has, _ := x.Where("uid=?", uid).And("org_id=?", orgID).Get(new(OrgUser))
	return has
}

// IsPublicMembership returns true if given user public his/her membership.
func IsPublicMembership(orgID, uid int64) bool {
	has, _ := x.Where("uid=?", uid).And("org_id=?", orgID).And("is_public=?", true).Get(new(OrgUser))
	return has
}

func getOrgsByUserID(sess *xorm.Session, userID int64, showAll bool) ([]*User, error) {
	orgs := make([]*User, 0, 10)
	if !showAll {
		sess.And("`org_user`.is_public=?", true)
	}
	return orgs, sess.And("`org_user`.uid=?", userID).
		Join("INNER", "`org_user`", "`org_user`.org_id=`user`.id").Find(&orgs)
}

// GetOrgsByUserID returns a list of organizations that the given user ID
// has joined.
func GetOrgsByUserID(userID int64, showAll bool) ([]*User, error) {
	return getOrgsByUserID(x.NewSession(), userID, showAll)
}

func getOwnedOrgsByUserID(sess *xorm.Session, userID int64) ([]*User, error) {
	orgs := make([]*User, 0, 10)
	return orgs, sess.Where("`org_user`.uid=?", userID).And("`org_user`.is_owner=?", true).
		Join("INNER", "`org_user`", "`org_user`.org_id=`user`.id").Find(&orgs)
}

// GetOwnedOrgsByUserID returns a list of organizations are owned by given user ID.
func GetOwnedOrgsByUserID(userID int64) ([]*User, error) {
	sess := x.NewSession()
	return getOwnedOrgsByUserID(sess, userID)
}

// GetOwnedOrganizationsByUserIDDesc returns a list of organizations are owned by
// given user ID, ordered descending by the given condition.
func GetOwnedOrgsByUserIDDesc(userID int64, desc string) ([]*User, error) {
	sess := x.NewSession()
	return getOwnedOrgsByUserID(sess.Desc(desc), userID)
}

func getOrgUsersByOrgID(e Engine, orgID int64, limit int) ([]*OrgUser, error) {
	orgUsers := make([]*OrgUser, 0, 10)

	sess := e.Where("org_id=?", orgID)
	if limit > 0 {
		sess = sess.Limit(limit)
	}
	return orgUsers, sess.Find(&orgUsers)
}

// GetOrgUsersByOrgID returns all organization-user relations by organization ID.
func GetOrgUsersByOrgID(orgID int64, limit int) ([]*OrgUser, error) {
	return getOrgUsersByOrgID(x, orgID, limit)
}

// ChangeOrgUserStatus changes public or private membership status.
func ChangeOrgUserStatus(orgID, uid int64, public bool) error {
	ou := new(OrgUser)
	has, err := x.Where("uid=?", uid).And("org_id=?", orgID).Get(ou)
	if err != nil {
		return err
	} else if !has {
		return nil
	}

	ou.IsPublic = public
	_, err = x.Id(ou.ID).AllCols().Update(ou)
	return err
}

// AddOrgUser adds new user to given organization.
func AddOrgUser(orgID, uid int64) error {
	if IsOrganizationMember(orgID, uid) {
		return nil
	}

	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}

	ou := &OrgUser{
		UID:   uid,
		OrgID: orgID,
	}

	if _, err := sess.Insert(ou); err != nil {
		return err
	} else if _, err = sess.Exec("UPDATE `user` SET num_members = num_members + 1 WHERE id = ?", orgID); err != nil {
		return err
	}

	return sess.Commit()
}

// RemoveOrgUser removes user from given organization.
func RemoveOrgUser(orgID, userID int64) error {
	ou := new(OrgUser)

	has, err := x.Where("uid=?", userID).And("org_id=?", orgID).Get(ou)
	if err != nil {
		return fmt.Errorf("get org-user: %v", err)
	} else if !has {
		return nil
	}

	user, err := Handle.Users().GetByID(context.TODO(), userID)
	if err != nil {
		return fmt.Errorf("GetUserByID [%d]: %v", userID, err)
	}
	org, err := Handle.Users().GetByID(context.TODO(), orgID)
	if err != nil {
		return fmt.Errorf("GetUserByID [%d]: %v", orgID, err)
	}

	// FIXME: only need to get IDs here, not all fields of repository.
	repos, _, err := org.GetUserRepositories(user.ID, 1, org.NumRepos)
	if err != nil {
		return fmt.Errorf("GetUserRepositories [%d]: %v", user.ID, err)
	}

	// Check if the user to delete is the last member in owner team.
	if IsOrganizationOwner(orgID, userID) {
		t, err := org.GetOwnerTeam()
		if err != nil {
			return err
		}
		if t.NumMembers == 1 {
			return ErrLastOrgOwner{UID: userID}
		}
	}

	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}

	if _, err := sess.ID(ou.ID).Delete(ou); err != nil {
		return err
	} else if _, err = sess.Exec("UPDATE `user` SET num_members=num_members-1 WHERE id=?", orgID); err != nil {
		return err
	}

	// Delete all repository accesses and unwatch them.
	repoIDs := make([]int64, 0, len(repos))
	for i := range repos {
		repoIDs = append(repoIDs, repos[i].ID)
		if err = watchRepo(sess, user.ID, repos[i].ID, false); err != nil {
			return err
		}
	}

	if len(repoIDs) > 0 {
		if _, err = sess.Where("user_id = ?", user.ID).In("repo_id", repoIDs).Delete(new(Access)); err != nil {
			return err
		}
	}

	// Delete member in his/her teams.
	teams, err := getUserTeams(sess, org.ID, user.ID)
	if err != nil {
		return err
	}
	for _, t := range teams {
		if err = removeTeamMember(sess, org.ID, t.ID, user.ID); err != nil {
			return err
		}
	}

	return sess.Commit()
}

func removeOrgRepo(e Engine, orgID, repoID int64) error {
	_, err := e.Delete(&TeamRepo{
		OrgID:  orgID,
		RepoID: repoID,
	})
	return err
}

// RemoveOrgRepo removes all team-repository relations of given organization.
func RemoveOrgRepo(orgID, repoID int64) error {
	return removeOrgRepo(x, orgID, repoID)
}

func (org *User) getUserTeams(e Engine, userID int64, cols ...string) ([]*Team, error) {
	teams := make([]*Team, 0, org.NumTeams)
	return teams, e.Where("team_user.org_id = ?", org.ID).
		And("team_user.uid = ?", userID).
		Join("INNER", "team_user", "team_user.team_id = team.id").
		Cols(cols...).Find(&teams)
}

// GetUserTeamIDs returns of all team IDs of the organization that user is member of.
func (org *User) GetUserTeamIDs(userID int64) ([]int64, error) {
	teams, err := org.getUserTeams(x, userID, "team.id")
	if err != nil {
		return nil, fmt.Errorf("getUserTeams [%d]: %v", userID, err)
	}

	teamIDs := make([]int64, len(teams))
	for i := range teams {
		teamIDs[i] = teams[i].ID
	}
	return teamIDs, nil
}

// GetTeams returns all teams that belong to organization,
// and that the user has joined.
func (org *User) GetUserTeams(userID int64) ([]*Team, error) {
	return org.getUserTeams(x, userID)
}

// GetUserRepositories returns a range of repositories in organization which the user has access to,
// and total number of records based on given condition.
func (org *User) GetUserRepositories(userID int64, page, pageSize int) ([]*Repository, int64, error) {
	teamIDs, err := org.GetUserTeamIDs(userID)
	if err != nil {
		return nil, 0, fmt.Errorf("GetUserTeamIDs: %v", err)
	}
	if len(teamIDs) == 0 {
		// user has no team but "IN ()" is invalid SQL
		teamIDs = []int64{-1} // there is no team with id=-1
	}

	var teamRepoIDs []int64
	if err = x.Table("team_repo").In("team_id", teamIDs).Distinct("repo_id").Find(&teamRepoIDs); err != nil {
		return nil, 0, fmt.Errorf("get team repository IDs: %v", err)
	}
	if len(teamRepoIDs) == 0 {
		// team has no repo but "IN ()" is invalid SQL
		teamRepoIDs = []int64{-1} // there is no repo with id=-1
	}

	if page <= 0 {
		page = 1
	}
	repos := make([]*Repository, 0, pageSize)
	if err = x.Where("owner_id = ?", org.ID).
		And(builder.Or(
			builder.And(builder.Expr("is_private = ?", false), builder.Expr("is_unlisted = ?", false)),
			builder.In("id", teamRepoIDs))).
		Desc("updated_unix").
		Limit(pageSize, (page-1)*pageSize).
		Find(&repos); err != nil {
		return nil, 0, fmt.Errorf("get user repositories: %v", err)
	}

	repoCount, err := x.Where("owner_id = ?", org.ID).
		And(builder.Or(
			builder.Expr("is_private = ?", false),
			builder.In("id", teamRepoIDs))).
		Count(new(Repository))
	if err != nil {
		return nil, 0, fmt.Errorf("count user repositories: %v", err)
	}

	return repos, repoCount, nil
}

// GetUserMirrorRepositories returns mirror repositories of the organization which the user has access to.
func (org *User) GetUserMirrorRepositories(userID int64) ([]*Repository, error) {
	teamIDs, err := org.GetUserTeamIDs(userID)
	if err != nil {
		return nil, fmt.Errorf("GetUserTeamIDs: %v", err)
	}
	if len(teamIDs) == 0 {
		teamIDs = []int64{-1}
	}

	var teamRepoIDs []int64
	err = x.Table("team_repo").In("team_id", teamIDs).Distinct("repo_id").Find(&teamRepoIDs)
	if err != nil {
		return nil, fmt.Errorf("get team repository ids: %v", err)
	}
	if len(teamRepoIDs) == 0 {
		// team has no repo but "IN ()" is invalid SQL
		teamRepoIDs = []int64{-1} // there is no repo with id=-1
	}

	repos := make([]*Repository, 0, 10)
	if err = x.Where("owner_id = ?", org.ID).
		And("is_private = ?", false).
		Or(builder.In("id", teamRepoIDs)).
		And("is_mirror = ?", true). // Don't move up because it's an independent condition
		Desc("updated_unix").
		Find(&repos); err != nil {
		return nil, fmt.Errorf("get user repositories: %v", err)
	}
	return repos, nil
}
