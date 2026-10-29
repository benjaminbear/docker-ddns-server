package handler

import "github.com/benjaminbear/docker-ddns-server/dyndns/nswrapper"

func (h *Handler) removeDNSRecords(names []string, zone string) error {
	if h.deleteRecords != nil {
		return h.deleteRecords(names, zone, h.AllowWildcard)
	}
	return nswrapper.DeleteRecords(names, zone, h.AllowWildcard)
}
