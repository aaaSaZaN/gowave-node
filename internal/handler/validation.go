package handler

import (
	"errors"
	"net/netip"
	"regexp"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validType(t string) bool {
	return t == "vless" || t == "trojan" || t == "shadowsocks" || t == "shadowsocks22" || t == "hysteria"
}
func (r *AddUserRequest) Validate() error {
	if len(r.Data) == 0 || !uuidPattern.MatchString(r.HashData.VlessUUID) {
		return errors.New("data and a valid hash UUID are required")
	}
	if r.HashData.PrevVlessUUID != nil && !uuidPattern.MatchString(*r.HashData.PrevVlessUUID) {
		return errors.New("invalid previous hash UUID")
	}
	user := r.Data[0].Username
	for _, u := range r.Data {
		if !validType(u.Type) || u.Tag == "" || u.Username == "" || u.Username != user {
			return errors.New("invalid inbound user")
		}
		if u.Type == "vless" {
			if !uuidPattern.MatchString(u.UUID) || (u.Flow != "" && u.Flow != "xtls-rprx-vision") {
				return errors.New("invalid VLESS account")
			}
		} else if u.Password == "" {
			return errors.New("password is required")
		}
	}
	return nil
}
func (r *RemoveUserRequest) Validate() error {
	if r.Username == "" || !uuidPattern.MatchString(r.HashData.VlessUUID) {
		return errors.New("username and hash UUID are required")
	}
	return nil
}
func (r *AddUsersRequest) Validate() error {
	if r.Users == nil || r.AffectedInboundTags == nil {
		return errors.New("users and affectedInboundTags are required")
	}
	for _, u := range r.Users {
		if u.UserData.UserID == "" || !uuidPattern.MatchString(u.UserData.HashUUID) || !uuidPattern.MatchString(u.UserData.VlessUUID) || u.InboundData == nil {
			return errors.New("invalid user data")
		}
		for _, i := range u.InboundData {
			if !validType(i.Type) || i.Tag == "" || (i.Flow != "" && i.Flow != "xtls-rprx-vision") {
				return errors.New("invalid inbound data")
			}
		}
	}
	return nil
}
func (r *RemoveUsersRequest) Validate() error {
	if r.Users == nil {
		return errors.New("users are required")
	}
	for _, u := range r.Users {
		if u.UserID == "" || !uuidPattern.MatchString(u.HashUUID) {
			return errors.New("invalid user data")
		}
	}
	return nil
}
func (r *DropUsersConnectionsRequest) Validate() error {
	if len(r.UserIDs) == 0 {
		return errors.New("userIds are required")
	}
	for _, u := range r.UserIDs {
		if u == "" {
			return errors.New("invalid userId")
		}
	}
	return nil
}
func (r *DropIPsRequest) Validate() error {
	if r.IPs == nil {
		return errors.New("ips are required")
	}
	for _, ip := range r.IPs {
		if _, err := netip.ParseAddr(ip); err != nil {
			return err
		}
	}
	return nil
}
