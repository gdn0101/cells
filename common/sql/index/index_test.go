package index

import (
	"context"
	"fmt"
	"github.com/pydio/cells/common/proto/tree"
	servicecontext "github.com/pydio/cells/common/service/context"
	"github.com/pydio/cells/common/sql"
	"github.com/pydio/cells/common/utils/mtree"
	"github.com/pydio/cells/x/configx"
)

var (
	options configx.Values = configx.New()

	mockNode   *mtree.TreeNode
	updateNode *mtree.TreeNode

	mockLongNodeMPath       mtree.MPath
	mockLongNodeChild1MPath mtree.MPath
	mockLongNodeChild2MPath mtree.MPath

	mockLongNode       *mtree.TreeNode
	mockLongNodeChild1 *mtree.TreeNode
	mockLongNodeChild2 *mtree.TreeNode
)

func init() {
	mockLongNodeMPath = []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40}
	mockLongNodeChild1MPath = append(mockLongNodeMPath, 41)
	mockLongNodeChild2MPath = append(mockLongNodeMPath, 42)

	mockNode = NewNode(&tree.Node{
		Uuid: "ROOT",
		Type: tree.NodeType_COLLECTION,
	}, []uint64{1}, []string{""})

	updateNode = NewNode(&tree.Node{
		Uuid: "Update",
		Type: tree.NodeType_LEAF,
		Etag: "etag1",
		Size: 12,
	}, []uint64{1, 2}, []string{""})

	mockLongNode = NewNode(&tree.Node{
		Uuid: "mockLongNode",
		Type: tree.NodeType_COLLECTION,
	}, mockLongNodeMPath, []string{"mockLongNode"})

	mockLongNodeChild1 = NewNode(&tree.Node{
		Uuid: "mockLongNodeChild1",
		Type: tree.NodeType_LEAF,
		Size: 25,
	}, mockLongNodeChild1MPath, []string{"mockLongNodeChild1"})

	mockLongNodeChild2 = NewNode(&tree.Node{
		Uuid: "mockLongNodeChild2",
		Type: tree.NodeType_LEAF,
		Size: 27,
	}, mockLongNodeChild2MPath, []string{"mockLongNodeChild2"})
}

func getSQLDAO(ctx context.Context) sql.DAO {
	return servicecontext.GetDAO(ctx).(sql.DAO)
}

func getDAO(ctx context.Context) DAO {
	return servicecontext.GetDAO(ctx).(DAO)
}

func printTree(ctx context.Context) {
	// query
	rows, err := getSQLDAO(ctx).DB().Query("SELECT uuid, name, mpath1, mpath2, mpath3, mpath4 FROM test_idx_tree")
	if err != nil {
		fmt.Println(err)
		return
	}

		for rows.Next() {
			var (
				uuid string
				name string
				mpath1 string
				mpath2 string
				mpath3 string
				mpath4 string
			)
			err := rows.Scan(&uuid, &name, &mpath1, &mpath2, &mpath3, &mpath4)
			fmt.Println(uuid, name, mpath1, mpath2, mpath3, mpath4, err)

		}
}
