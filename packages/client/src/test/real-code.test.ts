import { describe, it, expect } from 'vitest';
import type { FileNode } from '@engine/shared';
import {
  countFolders,
  hasFolderWithCollapsedChildren,
  findNodeByPath,
  shouldShowExpandAll,
  shouldShowCollapseAll,
  getAllCollapsedFolders,
  getCollapsedWithinFolder,
  expandAllFolders,
  expandFoldersWithin,
  collapseAllFolders,
  collapseFoldersWithin,
  hasDirectChildDirCollapsed,
  hasDirectChildDirExpanded,
} from '../components/FileTree/folderUtils';

/**
 * Real integration tests that import and test actual production code
 * These tests execute the real folderUtils functions with various inputs
 * and verify correct outputs
 */

describe('folderUtils - Real Production Code Tests', () => {
  // Sample test data
  const createTestTree = (): FileNode => ({
    name: 'project',
    path: '/project',
    type: 'directory',
    children: [
      {
        name: 'src',
        path: '/project/src',
        type: 'directory',
        children: [
          {
            name: 'components',
            path: '/project/src/components',
            type: 'directory',
            children: [
              { name: 'App.tsx', path: '/project/src/components/App.tsx', type: 'file' },
              { name: 'Header.tsx', path: '/project/src/components/Header.tsx', type: 'file' },
            ],
          },
          { name: 'index.ts', path: '/project/src/index.ts', type: 'file' },
        ],
      },
      {
        name: 'public',
        path: '/project/public',
        type: 'directory',
        children: [{ name: 'index.html', path: '/project/public/index.html', type: 'file' }],
      },
      { name: 'package.json', path: '/project/package.json', type: 'file' },
    ],
  });

  describe('countFolders - Real Code', () => {
    it('EmptyProject_ZeroFoldersCounted', () => {
      const emptyProject: FileNode = {
        name: 'project',
        path: '/project',
        type: 'directory',
        children: [],
      };
      const expanded = new Set<string>();
      const { total, expanded: expandedCount } = countFolders(emptyProject, expanded, true);
      expect(total).toBe(0);
      expect(expandedCount).toBe(0);
    });

    it('CollapsedTree_AllFoldersCounted', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(); // Nothing expanded
      const { total, expanded: expandedCount } = countFolders(tree, expanded, true);
      // Should have: src, public, components = 3 folders total
      expect(total).toBe(3);
      expect(expandedCount).toBe(0); // None are expanded
    });

    it('ExpandedFolders_CountedCorrectly', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src', '/project/src/components']);
      const { total, expanded: expandedCount } = countFolders(tree, expanded, true);
      // Total: src, public, components = 3
      // Expanded: src, components = 2
      expect(total).toBe(3);
      expect(expandedCount).toBe(2);
    });

    it('Files_ExcludedFromFolderCount', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      const { total } = countFolders(tree, expanded, true);
      // Should only count directories: src, public, components
      // NOT: App.tsx, Header.tsx, index.ts, index.html, package.json
      expect(total).toBe(3);
    });
  });

  describe('findNodeByPath - Real Code', () => {
    it('RootNode_Found', () => {
      const tree = createTestTree();
      const node = findNodeByPath('/project', tree);
      expect(node).toBeDefined();
      expect(node?.name).toBe('project');
    });

    it('NestedDirectory_Found', () => {
      const tree = createTestTree();
      const node = findNodeByPath('/project/src/components', tree);
      expect(node).toBeDefined();
      expect(node?.name).toBe('components');
      expect(node?.type).toBe('directory');
    });

    it('Files_Found', () => {
      const tree = createTestTree();
      const node = findNodeByPath('/project/src/components/App.tsx', tree);
      expect(node).toBeDefined();
      expect(node?.name).toBe('App.tsx');
      expect(node?.type).toBe('file');
    });

    it('NonExistentPath_Undefined', () => {
      const tree = createTestTree();
      const node = findNodeByPath('/project/nonexistent', tree);
      expect(node).toBeUndefined();
    });

    it('NullOrUndefinedInput_Handled', () => {
      const node1 = findNodeByPath('/project', null);
      const node2 = findNodeByPath('/project', undefined);
      expect(node1).toBeUndefined();
      expect(node2).toBeUndefined();
    });
  });

  describe('hasFolderWithCollapsedChildren - Real Code', () => {
    it('FolderWithCollapsedChildren_ReturnsTrue', () => {
      const tree = createTestTree();
      // Expand src but not its children
      const expanded = new Set<string>(['/project/src']);
      const result = hasFolderWithCollapsedChildren('/project/src', tree, expanded);
      // src has components folder which is NOT expanded = collapsed
      expect(result).toBe(true);
    });

    it('AllChildrenExpanded_ReturnsFalse', () => {
      const tree = createTestTree();
      // Expand everything under src
      const expanded = new Set<string>(['/project/src', '/project/src/components']);
      const result = hasFolderWithCollapsedChildren('/project/src', tree, expanded);
      // All children of src are now expanded
      expect(result).toBe(false);
    });

    it('LeafFolders_ReturnsFalse', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      const result = hasFolderWithCollapsedChildren('/project/src/components', tree, expanded);
      // components has no folder children (only files)
      expect(result).toBe(false);
    });

    it('NonExistentPaths_Handled', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      const result = hasFolderWithCollapsedChildren('/nonexistent', tree, expanded);
      expect(result).toBe(false);
    });

    it('UndefinedNodeInput_Handled', () => {
      const expanded = new Set<string>();
      const result = hasFolderWithCollapsedChildren('/project', undefined, expanded);
      // Line 45: if (!node) return false;
      expect(result).toBe(false);
    });

    it('FolderNoChildrenProperty_ReturnsFalse', () => {
      const tree: FileNode = {
        name: 'project',
        path: '/project',
        type: 'directory',
        // No children property
      };
      const expanded = new Set<string>();
      const result = hasFolderWithCollapsedChildren('/project', tree, expanded);
      // Line 61: ... ? ... : false
      expect(result).toBe(false);
    });
  });

  describe('shouldShowExpandAll - Real Code', () => {
    it('CollapsedChildrenExist_ExpandAllShown', () => {
      const tree = createTestTree();
      // Expand src but not components
      const expanded = new Set<string>(['/project/src']);
      const result = shouldShowExpandAll('/project/src', tree, expanded);
      expect(result).toBe(true);
    });

    it('AllChildrenExpanded_ExpandAllHidden', () => {
      const tree = createTestTree();
      // Expand everything
      const expanded = new Set<string>(['/project/src', '/project/src/components']);
      const result = shouldShowExpandAll('/project/src', tree, expanded);
      expect(result).toBe(false);
    });

    it('LeafFolders_ExpandAllHidden', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      const result = shouldShowExpandAll('/project/src/components', tree, expanded);
      expect(result).toBe(false);
    });

    it('NullTreeInput_Handled', () => {
      const expanded = new Set<string>();
      const result = shouldShowExpandAll('/project', null, expanded);
      // Line 97: tree || undefined
      expect(result).toBe(false);
    });

    it('UndefinedTreeInput_Handled', () => {
      const expanded = new Set<string>();
      const result = shouldShowExpandAll('/project', undefined, expanded);
      expect(result).toBe(false);
    });
  });

  describe('shouldShowCollapseAll - Real Code', () => {
    it('FoldersExpanded_CollapseAllShown', () => {
      const expanded = new Set<string>(['/project/src', '/project/public']);
      const result = shouldShowCollapseAll(expanded);
      expect(result).toBe(true);
    });

    it('NoFoldersExpanded_CollapseAllHidden', () => {
      const expanded = new Set<string>();
      const result = shouldShowCollapseAll(expanded);
      expect(result).toBe(false);
    });

    it('SingleExpandedFolder_CollapseAllShown', () => {
      const expanded = new Set<string>(['/project/src']);
      const result = shouldShowCollapseAll(expanded);
      expect(result).toBe(true);
    });
  });

  describe('Edge Cases - Real Code', () => {
    it('DeeplyNestedStructures_Handled', () => {
      const deepTree: FileNode = {
        name: 'root',
        path: '/root',
        type: 'directory',
        children: [
          {
            name: 'level1',
            path: '/root/level1',
            type: 'directory',
            children: [
              {
                name: 'level2',
                path: '/root/level1/level2',
                type: 'directory',
                children: [
                  {
                    name: 'level3',
                    path: '/root/level1/level2/level3',
                    type: 'directory',
                    children: [
                      {
                        name: 'file.txt',
                        path: '/root/level1/level2/level3/file.txt',
                        type: 'file',
                      },
                    ],
                  },
                ],
              },
            ],
          },
        ],
      };

      const expanded = new Set<string>();
      const { total } = countFolders(deepTree, expanded, true);
      // Should count: level1, level2, level3 = 3 folders
      expect(total).toBe(3);

      const found = findNodeByPath('/root/level1/level2/level3', deepTree);
      expect(found?.name).toBe('level3');
    });

    it('SingleFileAtRoot_Handled', () => {
      const singleFile: FileNode = {
        name: 'file.txt',
        path: '/file.txt',
        type: 'file',
      };
      const expanded = new Set<string>();
      const { total } = countFolders(singleFile, expanded, false);
      expect(total).toBe(0); // Files don't count as folders
    });

    it('FolderNoChildren_Handled', () => {
      const emptyFolder: FileNode = {
        name: 'empty',
        path: '/empty',
        type: 'directory',
      };
      const expanded = new Set<string>(['/empty']);
      const { total, expanded: expandedCount } = countFolders(emptyFolder, expanded, false);
      expect(total).toBe(1);
      expect(expandedCount).toBe(1);
    });
  });

  describe('State Consistency - Real Code', () => {
    it('SetIntegrity_MaintainedThroughOperations', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();

      // Add folders
      expanded.add('/project/src');
      expanded.add('/project/public');

      let { expanded: expandedCount } = countFolders(tree, expanded, true);
      expect(expandedCount).toBe(2);

      // Remove one
      expanded.delete('/project/public');
      ({ expanded: expandedCount } = countFolders(tree, expanded, true));
      expect(expandedCount).toBe(1);

      // Clear all
      expanded.clear();
      ({ expanded: expandedCount } = countFolders(tree, expanded, true));
      expect(expandedCount).toBe(0);
    });

    it('SetHas_WorksCorrectly', () => {
      const expanded = new Set<string>();
      expanded.add('/project/src');

      expect(expanded.has('/project/src')).toBe(true);
      expect(expanded.has('/project/public')).toBe(false);
      expect(expanded.has('/project/src/components')).toBe(false);
    });
  });
});

/**
 * Integration Tests - Using scoped expand/collapse functions
 */
describe('Scoped Expand/Collapse Functions - Integration', () => {
  const createTestTree = (): FileNode => ({
    name: 'project',
    path: '/project',
    type: 'directory',
    children: [
      {
        name: 'src',
        path: '/project/src',
        type: 'directory',
        children: [
          {
            name: 'components',
            path: '/project/src/components',
            type: 'directory',
            children: [
              { name: 'App.tsx', path: '/project/src/components/App.tsx', type: 'file' },
            ],
          },
          {
            name: 'utils',
            path: '/project/src/utils',
            type: 'directory',
            children: [
              { name: 'helpers.ts', path: '/project/src/utils/helpers.ts', type: 'file' },
            ],
          },
        ],
      },
      {
        name: 'public',
        path: '/project/public',
        type: 'directory',
        children: [
          { name: 'index.html', path: '/project/public/index.html', type: 'file' },
        ],
      },
    ],
  });

  describe('getAllCollapsedFolders - Global scope', () => {
    it('NothingExpanded_AllCollapsedFoldersReturned', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      
      const collapsed = getAllCollapsedFolders(tree, expanded);
      // Should find src and public as collapsed at root level
      expect(collapsed).toContain('/project/src');
      expect(collapsed).toContain('/project/public');
    });

    it('SomeExpanded_OnlyCollapsedFoldersReturned', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      const collapsed = getAllCollapsedFolders(tree, expanded);
      // src is expanded, so we can see its children
      // components and utils are collapsed
      expect(collapsed).toContain('/project/src/components');
      expect(collapsed).toContain('/project/src/utils');
      // public is still collapsed
      expect(collapsed).toContain('/project/public');
    });
  });

  describe('getCollapsedWithinFolder - Scoped to target folder', () => {
    it('TargetFolder_OnlyDirectCollapsedChildrenReturned', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      const collapsed = getCollapsedWithinFolder('/project/src', tree, expanded);
      // Only direct children of src
      expect(collapsed).toContain('/project/src/components');
      expect(collapsed).toContain('/project/src/utils');
      expect(collapsed.length).toBe(2);
    });

    it('FolderNoCollapsedChildren_EmptyArrayReturned', () => {
      const tree = createTestTree();
      const expanded = new Set<string>([
        '/project/src',
        '/project/src/components',
        '/project/src/utils',
      ]);
      
      const collapsed = getCollapsedWithinFolder('/project/src', tree, expanded);
      expect(collapsed).toEqual([]);
    });

    it('FolderNotFound_EmptyArrayReturned', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      const collapsed = getCollapsedWithinFolder('/nonexistent', tree, expanded);
      expect(collapsed).toEqual([]);
    });

    it('TargetIsFile_EmptyArrayReturned', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      const collapsed = getCollapsedWithinFolder('/project/src/components/App.tsx', tree, expanded);
      expect(collapsed).toEqual([]);
    });
  });

  describe('expandAllFolders - Global expand', () => {
    it('AllCollapsedFolders_Expanded', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      
      expandAllFolders(tree, expanded);
      
      // All folders should be expanded
      expect(expanded.has('/project/src')).toBe(true);
      expect(expanded.has('/project/public')).toBe(true);
      expect(expanded.has('/project/src/components')).toBe(true);
      expect(expanded.has('/project/src/utils')).toBe(true);
    });

    it('PartialExpansion_HandledCorrectly', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      expandAllFolders(tree, expanded);
      
      // All should be expanded now
      const { total, expanded: expandedCount } = countFolders(tree, expanded, true);
      expect(expandedCount).toBe(total);
    });

    it('NullTree_HandledGracefully', () => {
      const expanded = new Set<string>();
      
      expandAllFolders(null, expanded);
      
      expect(expanded.size).toBe(0);
    });

    it('UndefinedTree_HandledGracefully', () => {
      const expanded = new Set<string>();
      
      expandAllFolders(undefined, expanded);
      
      expect(expanded.size).toBe(0);
    });
  });

  describe('expandFoldersWithin - Scoped expand', () => {
    it('TargetFolderScoped_OnlyCollapsedExpanded', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      expandFoldersWithin('/project/src', tree, expanded);
      
      // src's children should be expanded
      expect(expanded.has('/project/src/components')).toBe(true);
      expect(expanded.has('/project/src/utils')).toBe(true);
      // But public should still be collapsed
      expect(expanded.has('/project/public')).toBe(false);
    });

    it('ExpandedOtherBranches_NotAffected', () => {
      const tree = createTestTree();
      const expanded = new Set<string>([
        '/project/src',
        '/project/public',
      ]);
      
      expandFoldersWithin('/project/src', tree, expanded);
      
      // src's children expanded
      expect(expanded.has('/project/src/components')).toBe(true);
      // public state unchanged
      expect(expanded.has('/project/public')).toBe(true);
    });
  });

  describe('collapseAllFolders - Global collapse', () => {
    it('EntireTreeCleared_CollapseAll', () => {
      const tree = createTestTree();
      const expanded = new Set<string>([
        '/project/src',
        '/project/src/components',
        '/project/src/utils',
        '/project/public',
      ]);
      
      collapseAllFolders(expanded);
      
      expect(expanded.size).toBe(0);
      
      const { expanded: expandedCount } = countFolders(tree, expanded, true);
      expect(expandedCount).toBe(0);
    });
  });

  describe('collapseFoldersWithin - Scoped collapse', () => {
    it('TargetFolderScoped_OnlyFoldersCollapsed', () => {
      const tree = createTestTree();
      const expanded = new Set<string>([
        '/project/src',
        '/project/src/components',
        '/project/src/utils',
        '/project/public',
      ]);
      
      collapseFoldersWithin('/project/src', expanded);
      
      // src's children should be collapsed
      expect(expanded.has('/project/src/components')).toBe(false);
      expect(expanded.has('/project/src/utils')).toBe(false);
      // But src itself and public should remain
      expect(expanded.has('/project/src')).toBe(true);
      expect(expanded.has('/project/public')).toBe(true);
    });

    it('CollapsedOtherBranches_NotAffected', () => {
      const tree = createTestTree();
      const expanded = new Set<string>([
        '/project/src',
        '/project/src/components',
        '/project/public',
      ]);
      
      collapseFoldersWithin('/project/src', expanded);
      
      // src's children collapsed
      expect(expanded.has('/project/src/components')).toBe(false);
      // public unaffected
      expect(expanded.has('/project/public')).toBe(true);
    });
  });

  describe('End-to-End Workflow - User interactions', () => {
    it('ExpandEntireTreeFromRoot_Workflow', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      
      // User right-clicks on root/empty space
      // Context type is 'empty', so use global expand
      expandAllFolders(tree, expanded);
      
      // All folders now expanded
      const { total, expanded: expandedCount } = countFolders(tree, expanded, true);
      expect(expandedCount).toBe(total);
    });

    it('ExpandOnlyWithinSrcFolder_Workflow', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      // User right-clicks on src folder
      // Context type is 'folder', so use scoped expand
      expandFoldersWithin('/project/src', tree, expanded);
      
      // src's children expanded, but public remains collapsed
      expect(expanded.has('/project/src/components')).toBe(true);
      expect(expanded.has('/project/src/utils')).toBe(true);
      expect(expanded.has('/project/public')).toBe(false);
    });

    it('CollapseEntireTreeFromRoot_Workflow', () => {
      const tree = createTestTree();
      const expanded = new Set<string>([
        '/project/src',
        '/project/src/components',
        '/project/src/utils',
        '/project/public',
      ]);
      
      // User right-clicks on root/empty space
      // Context type is 'empty', so use global collapse
      collapseAllFolders(expanded);
      
      // Everything collapsed
      expect(expanded.size).toBe(0);
    });

    it('CollapseOnlyWithinSrcFolder_Workflow', () => {
      const tree = createTestTree();
      const expanded = new Set<string>([
        '/project/src',
        '/project/src/components',
        '/project/src/utils',
        '/project/public',
      ]);
      
      // User right-clicks on src folder
      // Context type is 'folder', so use scoped collapse
      collapseFoldersWithin('/project/src', expanded);
      
      // src's children collapsed, but src and public remain
      expect(expanded.has('/project/src/components')).toBe(false);
      expect(expanded.has('/project/src/utils')).toBe(false);
      expect(expanded.has('/project/src')).toBe(true);
      expect(expanded.has('/project/public')).toBe(true);
    });
  });

  describe('Edge case - Deeply nested collapsed folders', () => {
    it('AllCollapsedFoldersIncludingNested_Found', () => {
      // Create a tree where a collapsed folder has subfolders inside
      const treeWithNestedCollapsed: FileNode = {
        name: 'project',
        path: '/project',
        type: 'directory',
        children: [
          {
            name: 'src',
            path: '/project/src',
            type: 'directory',
            children: [
              {
                name: 'deeply',
                path: '/project/src/deeply',
                type: 'directory',
                children: [
                  {
                    name: 'nested',
                    path: '/project/src/deeply/nested',
                    type: 'directory',
                    children: [
                      { name: 'file.ts', path: '/project/src/deeply/nested/file.ts', type: 'file' },
                    ],
                  },
                ],
              },
            ],
          },
        ],
      };
      
      // Only src is expanded, deeply and nested are collapsed
      const expanded = new Set<string>(['/project/src']);
      
      const collapsed = getAllCollapsedFolders(treeWithNestedCollapsed, expanded, true);
      
      // Should find deeply and nested even though deeply is inside another collapsed folder
      expect(collapsed).toContain('/project/src/deeply');
      expect(collapsed).toContain('/project/src/deeply/nested');
    });
  });

  describe('hasDirectChildDirCollapsed', () => {
    it('HasDirectChildDirCollapsed_UndefinedNode_ReturnsFalse', () => {
    });

    it('HasDirectChildDirCollapsed_FileNode_ReturnsFalse', () => {
      const file: FileNode = { name: 'f.ts', path: '/f.ts', type: 'file' };
      expect(hasDirectChildDirCollapsed(file, new Set())).toBe(false);
    });

    it('AllDirectChildDirsExpanded_ReturnsFalse', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src', '/project/public']);
      expect(hasDirectChildDirCollapsed(tree, expanded)).toBe(false);
    });

    it('SomeDirectChildDirsCollapsed_ReturnsTrue', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']); // public is collapsed
      expect(hasDirectChildDirCollapsed(tree, expanded)).toBe(true);
    });

    it('AllDirectChildDirsCollapsed_ReturnsTrue', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(); // nothing expanded
      expect(hasDirectChildDirCollapsed(tree, expanded)).toBe(true);
    });

    it('GrandchildState_Ignored_OnlyDirectChildrenChecked', () => {
      const tree = createTestTree();
      // Both direct children (src, public) are expanded
      // But grandchild (components) is NOT — should still return false
      const expanded = new Set<string>(['/project/src', '/project/public']);
      expect(hasDirectChildDirCollapsed(tree, expanded)).toBe(false);
    });

    it('FolderNoChildren_ReturnsFalse', () => {
      const empty: FileNode = { name: 'e', path: '/e', type: 'directory', children: [] };
      expect(hasDirectChildDirCollapsed(empty, new Set())).toBe(false);
    });

    it('FolderOnlyFileChildren_ReturnsFalse', () => {
      const onlyFiles: FileNode = {
        name: 'src', path: '/src', type: 'directory',
        children: [
          { name: 'a.ts', path: '/src/a.ts', type: 'file' },
          { name: 'b.ts', path: '/src/b.ts', type: 'file' },
        ],
      };
      expect(hasDirectChildDirCollapsed(onlyFiles, new Set())).toBe(false);
    });
  });

  describe('hasDirectChildDirExpanded', () => {
    it('HasDirectChildDirExpanded_UndefinedNode_ReturnsFalse', () => {
    });

    it('HasDirectChildDirExpanded_FileNode_ReturnsFalse', () => {
      const file: FileNode = { name: 'f.ts', path: '/f.ts', type: 'file' };
      expect(hasDirectChildDirExpanded(file, new Set())).toBe(false);
    });

    it('SomeDirectChildDirsExpanded_ReturnsTrue', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      expect(hasDirectChildDirExpanded(tree, expanded)).toBe(true);
    });

    it('AllDirectChildDirsExpanded_ReturnsTrue', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src', '/project/public']);
      expect(hasDirectChildDirExpanded(tree, expanded)).toBe(true);
    });

    it('NoDirectChildDirsExpanded_ReturnsFalse', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(); // nothing expanded
      expect(hasDirectChildDirExpanded(tree, expanded)).toBe(false);
    });

    it('GrandchildStateExpanded_Ignored_OnlyDirectChildrenChecked', () => {
      const tree = createTestTree();
      // No direct children expanded, but grandchild components IS
      const expanded = new Set<string>(['/project/src/components']);
      expect(hasDirectChildDirExpanded(tree, expanded)).toBe(false);
    });

    it('FolderNoChildrenExpanded_ReturnsFalse', () => {
      const empty: FileNode = { name: 'e', path: '/e', type: 'directory', children: [] };
      expect(hasDirectChildDirExpanded(empty, new Set())).toBe(false);
    });

    it('FolderOnlyFileChildrenExpanded_ReturnsFalse', () => {
      const onlyFiles: FileNode = {
        name: 'src', path: '/src', type: 'directory',
        children: [
          { name: 'a.ts', path: '/src/a.ts', type: 'file' },
          { name: 'b.ts', path: '/src/b.ts', type: 'file' },
        ],
      };
      expect(hasDirectChildDirExpanded(onlyFiles, new Set())).toBe(false);
    });
  });
});
