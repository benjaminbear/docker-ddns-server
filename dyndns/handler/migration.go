package handler

import (
	"github.com/benjaminbear/docker-ddns-server/dyndns/model"
	"gorm.io/gorm"
)

// migrateDatabase preserves the oldest active duplicate (otherwise oldest row),
// reassigns its dependent rows, then installs the GORM v2 unique index.
// Everything is transactional, including schema changes under SQLite.
func migrateDatabase(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Migrator().HasTable(&model.Host{}) {
			var hosts []model.Host
			if err := tx.Unscoped().Order("deleted_at IS NOT NULL, id").Find(&hosts).Error; err != nil {
				return err
			}
			seen := make(map[[2]string]uint)
			for _, host := range hosts {
				key := [2]string{host.Hostname, host.Domain}
				keeper, duplicate := seen[key]
				if !duplicate {
					seen[key] = host.ID
					continue
				}
				for _, relation := range []struct {
					table  interface{}
					column string
				}{
					{&model.CName{}, "target_id"}, {&model.Log{}, "host_id"},
				} {
					if tx.Migrator().HasTable(relation.table) {
						if err := tx.Unscoped().Model(relation.table).Where(relation.column+" = ?", host.ID).Update(relation.column, keeper).Error; err != nil {
							return err
						}
					}
				}
				if err := tx.Unscoped().Delete(&host).Error; err != nil {
					return err
				}
			}
		}
		return tx.AutoMigrate(&model.Host{}, &model.CName{}, &model.Log{}, &model.FailedAuth{}, &model.BlockedIP{}, &model.Record{}, &model.ProxyLog{}, &model.LogCursor{})
	})
}
