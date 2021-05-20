package service

import (
	"errors"
	"github.com/casbin/casbin/v2"

	"github.com/go-admin-team/go-admin-core/sdk/service"
	"gorm.io/gorm"

	"go-admin/app/admin/models"
	"go-admin/app/admin/service/dto"
	cDto "go-admin/common/dto"
)

type SysRole struct {
	service.Service
}

// GetSysRolePage 获取SysRole列表
func (e *SysRole) GetSysRolePage(c *dto.SysRoleSearch, list *[]models.SysRole, count *int64) error {
	var err error
	var data models.SysRole

	err = e.Orm.Model(&data).Preload("SysMenu").
		Scopes(
			cDto.MakeCondition(c.GetNeedSearch()),
			cDto.Paginate(c.GetPageSize(), c.GetPageIndex()),
		).
		Find(list).Limit(-1).Offset(-1).
		Count(count).Error
	if err != nil {
		e.Log.Errorf("db error:%s", err)
		return err
	}
	return nil
}

// GetSysRole 获取SysRole对象
func (e *SysRole) GetSysRole(d *dto.SysRoleById, model *models.SysRole) error {
	var err error

	db := e.Orm.First(model, d.GetId())
	err = db.Error
	if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
		err = errors.New("查看对象不存在或无权查看")
		e.Log.Errorf("db error:%s", err)
		return err
	}
	if err != nil {
		e.Log.Errorf("db error:%s", err)
		return err
	}
	model.MenuIds, err = e.GetRoleMenuId(model.RoleId)
	if err != nil {
		e.Log.Errorf("get menuIds error, %s", err.Error())
		return err
	}
	return nil
}

// InsertSysRole 创建SysRole对象
func (e *SysRole) InsertSysRole(c *dto.SysRoleControl) error {
	var err error
	var data models.SysRole
	var dataMenu []models.SysMenu
	err = e.Orm.Preload("SysApi").Where("menu_id in ?", c.MenuIds).Find(&dataMenu).Error
	if err != nil {
		e.Log.Errorf("db error:%s", err)
		return err
	}
	c.SysMenu = dataMenu
	c.Generate(&data)
	tx := e.Orm.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()

	err = tx.Model(&data).
		Create(c).Error
	if err != nil {
		e.Log.Errorf("db error:%s", err)
		return err
	}
	if len(c.MenuIds) > 0 {
		s := SysRoleMenu{}
		s.Orm = e.Orm
		s.Log = e.Log
		err = s.ReloadRule(tx, c.RoleId, c.MenuIds)
		if err != nil {
			e.Log.Errorf("reload casbin rule error, %", err.Error())
			return err
		}
	}
	return nil
}

// UpdateSysRole 修改SysRole对象
func (e *SysRole) UpdateSysRole(c *dto.SysRoleControl, cb *casbin.SyncedEnforcer) error {
	var err error

	tx := e.Orm.Debug().Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()
	var model = models.SysRole{}
	var mlist = make([]models.SysMenu, 0)
	e.Orm.First(&model, c.GetId())
	e.Orm.Preload("SysApi").Find(&mlist, c.MenuIds)
	c.Generate(&model)
	model.SysMenu = mlist
	db := e.Orm.Session(&gorm.Session{FullSaveAssociations: true}).Debug().Save(&model)

	if db.Error != nil {
		e.Log.Errorf("db error:%s", err)
		return err
	}
	if db.RowsAffected == 0 {
		return errors.New("无权更新该数据")
	}
	for _, menu := range mlist {
		for _, api := range menu.SysApi {
			_, err = cb.AddNamedPolicy("p", model.RoleKey, api.Path, api.Action)
		}
	}

	_ = cb.SavePolicy()
	return nil
}

// RemoveSysRole 删除SysRole
func (e *SysRole) RemoveSysRole(d *dto.SysRoleById) error {
	var err error
	var data models.SysRole

	tx := e.Orm.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()

	s := SysRoleMenu{}
	s.Orm = tx
	s.Log = e.Log
	for _, roleId := range d.Ids {
		err = s.DeleteRoleMenu(tx, roleId)
		if err != nil {
			e.Log.Errorf("insert role menu error, %", err.Error())
			return err
		}
	}
	db := tx.Model(&data).Delete(&data, d.Ids)
	if db.Error != nil {
		err = db.Error
		e.Log.Errorf("Delete error: %s", err)
		return err
	}
	if db.RowsAffected == 0 {
		err = errors.New("无权删除该数据")
		return err
	}
	return nil
}

// 获取角色对应的菜单ids
func (e *SysRole) GetRoleMenuId(roleId int) ([]int, error) {
	menuIds := make([]int, 0)
	menuList := make([]models.MenuIdList, 0)
	if err := e.Orm.Table("sys_role_menu").
		Select("sys_role_menu.menu_id").
		Where("role_id = ? ", roleId).
		Where(" sys_role_menu.menu_id not in(select sys_menu.parent_id from sys_role_menu "+
			"LEFT JOIN sys_menu on sys_menu.menu_id=sys_role_menu.menu_id where role_id =?  and parent_id is not null)", roleId).
		Find(&menuList).Error; err != nil {
		return nil, err
	}

	for i := 0; i < len(menuList); i++ {
		menuIds = append(menuIds, menuList[i].MenuId)
	}
	return menuIds, nil
}

func (e *SysRole) UpdateDataScope(c *models.SysRole) (err error) {
	tx := e.Orm.Begin()
	defer func() {
		if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()
	err = tx.Model(&models.SysRole{}).Where("role_id = ?", c.RoleId).Select("data_scope", "update_by").Updates(c).Error
	if err != nil {
		return err
	}

	err = tx.Where("role_id = ?", c.RoleId).Delete(&models.SysRoleDept{}).Error
	if err != nil {
		return err
	}

	if c.DataScope == "2" {
		deptRoles := make([]models.SysRoleDept, len(c.DeptIds))
		for i := range c.DeptIds {
			deptRoles[i] = models.SysRoleDept{
				RoleId: c.RoleId,
				DeptId: c.DeptIds[i],
			}
		}
		err = tx.Create(&deptRoles).Error
		if err != nil {
			return err
		}
	}
	return err
}