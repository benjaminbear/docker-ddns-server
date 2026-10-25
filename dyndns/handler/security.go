package handler

import (
	"strings"
	"time"

	"github.com/labstack/gommon/log"
	"github.com/w3K-one/docker-ddns-server/dyndns/model"
	"gorm.io/gorm"
)

const (
	MaxFailedAttempts = 3
	BlockDuration     = 168 * time.Hour // 7 days (1 week) in hours
	LookbackPeriod    = 72 * time.Hour  // Check failures in last 3 days
)

// LogFailedAuth records a failed authentication attempt
// WARNING: This logs passwords which is a security risk. Ensure database is properly secured.
// IMPORTANT: IP blocking only applies to admin panel attempts (/@/*), not API endpoints
func (h *Handler) LogFailedAuth(ipAddress, userAgent, path, username, password string, requestedHostname ...string) error {
	failedAuth := &model.FailedAuth{
		IPAddress: ipAddress,
		UserAgent: userAgent,
		Timestamp: time.Now(),
		Path:      path,
		Username:  username,
		Password:  password, // SECURITY WARNING: Storing attempted passwords
	}

	if len(requestedHostname) > 0 {
		hostname := []rune(strings.ToLower(strings.TrimSpace(requestedHostname[0])))
		if len(hostname) > 253 {
			hostname = hostname[:253]
		}
		failedAuth.Hostname = string(hostname)
	}

	if err := h.DB.Create(failedAuth).Error; err != nil {
		log.Error("Failed to log authentication failure:", err)
		return err
	}

	// CRITICAL: Only check for IP blocking if this was an admin panel attempt
	// API endpoints (like /nic/update, /update, /v2/update, /v3/update) should NOT trigger blocking
	if strings.HasPrefix(path, "/@/") {
		log.Infof("Admin panel failed auth from %s - checking for IP block", ipAddress)
		go h.CheckAndBlockIP(ipAddress)
	} else {
		log.Infof("API endpoint failed auth from %s on %s - NOT checking for IP block", ipAddress, path)
	}

	return nil
}

// CheckAndBlockIP checks if an IP has exceeded failed attempts and blocks it
// ONLY COUNTS FAILURES TO ADMIN PANEL (/@/*), NOT API ENDPOINTS
func (h *Handler) CheckAndBlockIP(ipAddress string) error {
	// Count failed attempts to ADMIN PANEL ONLY in the lookback period
	var count int64
	lookbackTime := time.Now().Add(-LookbackPeriod)

	err := h.DB.Model(&model.FailedAuth{}).
		Where("ip_address = ? AND timestamp > ? AND path LIKE '/@/%'", ipAddress, lookbackTime).
		Count(&count).Error

	if err != nil {
		log.Error("Failed to count authentication failures:", err)
		return err
	}

	log.Infof("IP %s has %d failed ADMIN PANEL attempts in last %v", ipAddress, count, LookbackPeriod)

	// If exceeded threshold, block the IP
	if count >= MaxFailedAttempts {
		return h.BlockIP(ipAddress, int(count), "Exceeded maximum failed admin authentication attempts")
	}

	return nil
}

// BlockIP adds an IP to the blocked list
func (h *Handler) BlockIP(ipAddress string, failureCount int, reason string) error {
	// Check if IP is already blocked
	var existingBlock model.BlockedIP
	err := h.DB.Where("ip_address = ?", ipAddress).First(&existingBlock).Error

	if err == nil {
		// Update existing block
		existingBlock.FailureCount = failureCount
		existingBlock.LastAttemptAt = time.Now()
		existingBlock.BlockedUntil = time.Now().Add(BlockDuration)
		existingBlock.Reason = reason

		if err := h.DB.Save(&existingBlock).Error; err != nil {
			log.Error("Failed to update blocked IP:", err)
			return err
		}

		log.Warnf("Updated block for IP %s (failures: %d)", ipAddress, failureCount)
		return nil
	}

	// Create new block
	blockedIP := &model.BlockedIP{
		IPAddress:     ipAddress,
		BlockedAt:     time.Now(),
		BlockedUntil:  time.Now().Add(BlockDuration),
		FailureCount:  failureCount,
		IsPermanent:   false,
		LastAttemptAt: time.Now(),
		Reason:        reason,
	}

	if err := h.DB.Create(blockedIP).Error; err != nil {
		log.Error("Failed to block IP:", err)
		return err
	}

	log.Warnf("Blocked IP %s for %v (failures: %d, reason: %s)",
		ipAddress, BlockDuration, failureCount, reason)

	return nil
}

// IsIPBlocked checks if an IP address is currently blocked
func (h *Handler) IsIPBlocked(ipAddress string) (bool, *model.BlockedIP, error) {
	var blockedIP model.BlockedIP
	err := h.DB.Where("ip_address = ?", ipAddress).First(&blockedIP).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil, nil
		}
		return false, nil, err
	}

	// Check if block is still active
	if blockedIP.IsBlocked() {
		return true, &blockedIP, nil
	}

	return false, nil, nil
}

// UnblockIP removes an IP from the blocked list
func (h *Handler) UnblockIP(ipAddress string) error {
	result := h.DB.Where("ip_address = ?", ipAddress).Delete(&model.BlockedIP{})
	if result.Error != nil {
		log.Error("Failed to unblock IP:", result.Error)
		return result.Error
	}

	log.Infof("Unblocked IP %s", ipAddress)
	return nil
}

// GetClientIP extracts the real client IP from the request
func GetClientIP(r interface{}) string {
	// This function can be enhanced to check X-Forwarded-For, X-Real-IP headers
	// For now, we'll use a simple extraction

	// You'll need to pass the Echo context here
	// This is a helper that should be called from middleware
	return ""
}

// CleanupExpiredBlocks removes expired blocks from the database
func (h *Handler) CleanupExpiredBlocks() error {
	result := h.DB.Where("is_permanent = ? AND blocked_until < ?", false, time.Now()).
		Delete(&model.BlockedIP{})

	if result.Error != nil {
		log.Error("Failed to cleanup expired blocks:", result.Error)
		return result.Error
	}

	if result.RowsAffected > 0 {
		log.Infof("Cleaned up %d expired IP blocks", result.RowsAffected)
	}

	return nil
}

// CleanupOldFailedAuths removes old failed authentication records
func (h *Handler) CleanupOldFailedAuths() error {
	// Keep records for 30 days
	cutoffTime := time.Now().Add(-30 * 24 * time.Hour)

	result := h.DB.Where("timestamp < ?", cutoffTime).Delete(&model.FailedAuth{})

	if result.Error != nil {
		log.Error("Failed to cleanup old failed auths:", result.Error)
		return result.Error
	}

	if result.RowsAffected > 0 {
		log.Infof("Cleaned up %d old failed authentication records", result.RowsAffected)
	}

	return nil
}

// GetBlockedIPs returns all currently blocked IPs
func (h *Handler) GetBlockedIPs() ([]model.BlockedIP, error) {
	var blockedIPs []model.BlockedIP
	err := h.DB.Order("blocked_at DESC").Find(&blockedIPs).Error
	return blockedIPs, err
}

// GetRecentFailedAuths returns recent failed authentication attempts
func (h *Handler) GetRecentFailedAuths(limit int) ([]model.FailedAuth, error) {
	var failedAuths []model.FailedAuth
	err := h.DB.Order("timestamp DESC").Limit(limit).Find(&failedAuths).Error
	return failedAuths, err
}
