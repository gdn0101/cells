/*
 * Copyright (c) 2018. Abstrium SAS <team (at) pydio.com>
 * This file is part of Pydio Cells.
 *
 * Pydio Cells is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * Pydio Cells is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with Pydio Cells.  If not, see <http://www.gnu.org/licenses/>.
 *
 * The latest code can be found at <https://pydio.com>.
 */
package index

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pydio/cells/common/proto/tree"
	"github.com/pydio/cells/common/sql"
	"github.com/pydio/cells/common/utils/mtree"
)

var (
	folderSizeCache = make(map[string]int64)
	folderSizeLock  = &sync.RWMutex{}
)

// FolderSizeCacheSQL implementation
type FolderSizeCacheSQL struct {
	DAO
}

func init() {
	queries["childrenSize"] = func(dao sql.DAO, mpathes ...string) (string, []interface{}) {
		sub, args := getMPathLike([]byte(mpathes[0]))
		return fmt.Sprintf(`
			select sum(size)
			FROM %%PREFIX%%_idx_tree
			WHERE %s AND level >= ? AND leaf=1`, sub), args
	}
}

func parents(p string) []string {
	var s []string
	paths := strings.Split(p, "/")
	current := ""
	for _, p := range paths {
		current = current + "/" + p
		s = append(s, current)
	}

	return s
}

// NewFolderSizeCacheDAO provides a middleware implementation of the index sql dao that removes duplicate entries of the .pydio file that have the same etag at the same level
func NewFolderSizeCacheDAO(dao DAO) DAO {
	//return dao
	return &FolderSizeCacheSQL{
		dao,
	}
}

// GetNode from path
func (dao *FolderSizeCacheSQL) GetNode(path mtree.MPath) (*mtree.TreeNode, error) {

	node, err := dao.DAO.GetNode(path)
	if err != nil {
		return nil, err
	}

	if node != nil && !node.IsLeaf() {
		dao.folderSize(node)
	}

	return node, nil
}

// GetNodeByUUID returns the node stored with the unique uuid
func (dao *FolderSizeCacheSQL) GetNodeByUUID(uuid string) (*mtree.TreeNode, error) {

	node, err := dao.DAO.GetNodeByUUID(uuid)
	if err != nil {
		return nil, err
	}

	if node != nil && !node.IsLeaf() {
		dao.folderSize(node)
	}

	return node, nil
}

// GetNodeChildren List
func (dao *FolderSizeCacheSQL) GetNodeChildren(path mtree.MPath) chan interface{} {
	c := make(chan interface{})

	go func() {
		defer close(c)

		cc := dao.DAO.GetNodeChildren(path)

		for obj := range cc {
			if node, ok := obj.(*mtree.TreeNode); ok {
				if node != nil && !node.IsLeaf() {
					dao.folderSize(node)
				}
			}
			c <- obj
		}
	}()

	return c
}

// GetNodeTree List from the path
func (dao *FolderSizeCacheSQL) GetNodeTree(path mtree.MPath) chan interface{} {
	c := make(chan interface{})

	go func() {
		defer close(c)

		cc := dao.DAO.GetNodeTree(path)

		for obj := range cc {
			if node, ok := obj.(*mtree.TreeNode); ok {
				if node != nil && !node.IsLeaf() {
					dao.folderSize(node)
				}
			}
			c <- obj
		}
	}()

	return c
}

func (dao *FolderSizeCacheSQL) Path(strpath string, create bool, reqNode ...*tree.Node) (mtree.MPath, []*mtree.TreeNode, error) {
	mpath, nodes, err := dao.DAO.Path(strpath, create, reqNode...)

	if create {
		go dao.invalidateMPathHierarchy(mpath, -1)
	}

	return mpath, nodes, err
}

// Add a node in the tree
func (dao *FolderSizeCacheSQL) AddNode(node *mtree.TreeNode) error {
	dao.invalidateMPathHierarchy(node.MPath, -1)
	return dao.DAO.AddNode(node)
}

// SetNode updates a node, including its tree position
func (dao *FolderSizeCacheSQL) SetNode(node *mtree.TreeNode) error {
	dao.invalidateMPathHierarchy(node.MPath, -1)
	return dao.DAO.SetNode(node)
}

// Remove a node from the tree
func (dao *FolderSizeCacheSQL) DelNode(node *mtree.TreeNode) error {
	dao.invalidateMPathHierarchy(node.MPath, -1)
	return dao.DAO.DelNode(node)
}

// MoveNodeTree move all the nodes belonging to a tree by calculating the new mpathes
func (dao *FolderSizeCacheSQL) MoveNodeTree(nodeFrom *mtree.TreeNode, nodeTo *mtree.TreeNode) error {
	root := nodeTo.MPath.CommonRoot(nodeFrom.MPath)

	dao.invalidateMPathHierarchy(nodeTo.MPath, len(root))
	dao.invalidateMPathHierarchy(nodeFrom.MPath, len(root))

	return dao.DAO.MoveNodeTree(nodeFrom, nodeTo)
}

func (dao *FolderSizeCacheSQL) GetSQLDAO() sql.DAO {
	return dao.DAO.GetSQLDAO()
}

func (dao *FolderSizeCacheSQL) invalidateMPathHierarchy(mpath mtree.MPath, level int) {

	parents := mpath.Parents()
	if level > -1 {
		parents = mpath.Parents()[level:]
	}

	for _, p := range parents {
		folderSizeLock.Lock()
		delete(folderSizeCache, p.String())
		folderSizeLock.Unlock()
	}
}

// Compute sizes from children files - Does not handle lock, should be
// used by other functions handling lock
func (dao *FolderSizeCacheSQL) folderSize(node *mtree.TreeNode) {

	mpath := node.MPath.String()

	folderSizeLock.RLock()
	size, ok := folderSizeCache[mpath]
	folderSizeLock.RUnlock()

	if ok {
		node.Size = size
		return
	}

	if stmt, args, e := dao.GetSQLDAO().GetStmtWithArgs("childrenSize", mpath); e == nil {
		row := stmt.QueryRow(append(args, len(node.MPath)+1)...)
		if row != nil {
			var size int64
			if er := row.Scan(&size); er == nil {
				node.Size = size

				folderSizeLock.Lock()
				folderSizeCache[mpath] = size
				folderSizeLock.Unlock()
			}
		}
	}
}
