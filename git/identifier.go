package git

import (
	"errors"
	"fmt"
	"github.com/smarterclayton/geard/config"
	"github.com/smarterclayton/geard/containers"
	"github.com/smarterclayton/geard/utils"
	"os/user"
	"path/filepath"
	"strings"
)

type RepoIdentifier containers.Identifier

const RepoIdentifierPrefix = "git-"

func NewIdentifierFromUser(u *user.User) (RepoIdentifier, error) {
	if !strings.HasPrefix(u.Username, RepoIdentifierPrefix) || u.Name != "Repository user" {
		return "", errors.New("Not a repository user")
	}
	id := strings.TrimLeft(u.Username, RepoIdentifierPrefix)
	containerId, err := containers.NewIdentifier(id)
	if err != nil {
		return "", err
	}
	return RepoIdentifier(containerId), nil
}

func (i RepoIdentifier) UnitPathFor() string {
	base := utils.IsolateContentPath(filepath.Join(config.ContainerBasePath(), "units"), string(i), "")
	return filepath.Join(filepath.Dir(base), i.UnitNameFor())
}

func (i RepoIdentifier) UnitNameFor() string {
	return fmt.Sprintf("%s%s.service", RepoIdentifierPrefix, i)
}

func (i RepoIdentifier) LoginFor() string {
	return fmt.Sprintf("%s%s", RepoIdentifierPrefix, i)
}

func (i RepoIdentifier) BaseHomePath() string {
	return utils.IsolateContentPathWithPerm(filepath.Join(config.ContainerBasePath(), fmt.Sprintf("%shome", RepoIdentifierPrefix)), string(i), "", 0775)
}

func (i RepoIdentifier) HomePath() string {
	return utils.IsolateContentPathWithPerm(filepath.Join(config.ContainerBasePath(), fmt.Sprintf("%shome", RepoIdentifierPrefix)), string(i), "home", 0775)
}

func (i RepoIdentifier) SshAccessBasePath() string {
	return utils.IsolateContentPathWithPerm(filepath.Join(config.ContainerBasePath(), "access", "git"), string(i), "", 0775)
}

func (i RepoIdentifier) AuthKeysPathFor() string {
	return filepath.Join(i.HomePath(), ".ssh", "authorized_keys")
}

func (i RepoIdentifier) RepositoryPathFor() string {
	return filepath.Join(config.ContainerBasePath(), "git", string(i))
}
