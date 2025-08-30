package links

import (
	"errors"
	"syscall"

	"github.com/vishvananda/netlink"
)

func IsLinkNotFoundError(err error) bool {
	var netlinkErr netlink.LinkNotFoundError
	return errors.Is(err, syscall.ENODEV) || errors.As(err, &netlinkErr)
}

func IsLinkExistsError(err error) bool {
	return errors.Is(err, syscall.EEXIST)
}
