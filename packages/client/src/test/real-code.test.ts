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
    it('counts zero folders in empty project', () => {
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

    it('counts all folders in collapsed tree', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(); // Nothing expanded
      const { total, expanded: expandedCount } = countFolders(tree, expanded, true);
      // Should have: src, public, components = 3 folders total
      expect(total).toBe(3);
      expect(expandedCount).toBe(0); // None are expanded
    });

    it('counts expanded folders correctly', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src', '/project/src/components']);
      const { total, expanded: expandedCount } = countFolders(tree, expanded, true);
      // Total: src, public, components = 3
      // Expanded: src, components = 2
      expect(total).toBe(3);
      expect(expandedCount).toBe(2);
    });

    it('excludes files from folder count', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      const { total } = countFolders(tree, expanded, true);
      // Should only count directories: src, public, components
      // NOT: App.tsx, Header.tsx, index.ts, index.html, package.json
      expect(total).toBe(3);
    });
  });

  describe('findNodeByPath - Real Code', () => {
    it('finds root node', () => {
      const tree = createTestTree();
      const node = findNodeByPath('/project', tree);
      expect(node).toBeDefined();
      expect(node?.name).toBe('project');
    });

    it('finds nested directory', () => {
      const tree = createTestTree();
      const node = findNodeByPath('/project/src/components', tree);
      expect(node).toBeDefined();
      expect(node?.name).toBe('components');
      expect(node?.type).toBe('directory');
    });

    it('finds files', () => {
      const tree = createTestTree();
      const node = findNodeByPath('/project/src/components/App.tsx', tree);
      expect(node).toBeDefined();
      expect(node?.name).toBe('App.tsx');
      expect(node?.type).toBe('file');
    });

    it('returns undefined for non-existent path', () => {
      const tree = createTestTree();
      const node = findNodeByPath('/project/nonexistent', tree);
      expect(node).toBeUndefined();
    });

    it('handles null/undefined input', () => {
      const node1 = findNodeByPath('/project', null);
      const node2 = findNodeByPath('/project', undefined);
      expect(node1).toBeUndefined();
      expect(node2).toBeUndefined();
    });
  });

  describe('hasFolderWithCollapsedChildren - Real Code', () => {
    it('returns true when folder has collapsed children', () => {
      const tree = createTestTree();
      // Expand src but not its children
      const expanded = new Set<string>(['/project/src']);
      const result = hasFolderWithCollapsedChildren('/project/src', tree, expanded);
      // src has components folder which is NOT expanded = collapsed
      expect(result).toBe(true);
    });

    it('returns false when all children are expanded', () => {
      const tree = createTestTree();
      // Expand everything under src
      const expanded = new Set<string>(['/project/src', '/project/src/components']);
      const result = hasFolderWithCollapsedChildren('/project/src', tree, expanded);
      // All children of src are now expanded
      expect(result).toBe(false);
    });

    it('returns false for leaf folders', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      const result = hasFolderWithCollapsedChildren('/project/src/components', tree, expanded);
      // components has no folder children (only files)
      expect(result).toBe(false);
    });

    it('handles non-existent paths', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      const result = hasFolderWithCollapsedChildren('/nonexistent', tree, expanded);
      expect(result).toBe(false);
    });

    it('handles undefined node input', () => {
      const expanded = new Set<string>();
      const result = hasFolderWithCollapsedChildren('/project', undefined, expanded);
      // Line 45: if (!node) return false;
      expect(result).toBe(false);
    });

    it('returns false for folder with no children property', () => {
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
    it('shows Expand All when collapsed children exist', () => {
      const tree = createTestTree();
      // Expand src but not components
      const expanded = new Set<string>(['/project/src']);
      const result = shouldShowExpandAll('/project/src', tree, expanded);
      expect(result).toBe(true);
    });

    it('hides Expand All when all children expanded', () => {
      const tree = createTestTree();
      // Expand everything
      const expanded = new Set<string>(['/project/src', '/project/src/components']);
      const result = shouldShowExpandAll('/project/src', tree, expanded);
      expect(result).toBe(false);
    });

    it('hides Expand All for leaf folders', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      const result = shouldShowExpandAll('/project/src/components', tree, expanded);
      expect(result).toBe(false);
    });

    it('handles null tree input', () => {
      const expanded = new Set<string>();
      const result = shouldShowExpandAll('/project', null, expanded);
      // Line 97: tree || undefined
      expect(result).toBe(false);
    });

    it('handles undefined tree input', () => {
      const expanded = new Set<string>();
      const result = shouldShowExpandAll('/project', undefined, expanded);
      expect(result).toBe(false);
    });
  });

  describe('shouldShowCollapseAll - Real Code', () => {
    it('shows Collapse All when folders are expanded', () => {
      const expanded = new Set<string>(['/project/src', '/project/public']);
      const result = shouldShowCollapseAll(expanded);
      expect(result).toBe(true);
    });

    it('hides Collapse All when no folders expanded', () => {
      const expanded = new Set<string>();
      const result = shouldShowCollapseAll(expanded);
      expect(result).toBe(false);
    });

    it('shows Collapse All even with single expanded folder', () => {
      const expanded = new Set<string>(['/project/src']);
      const result = shouldShowCollapseAll(expanded);
      expect(result).toBe(true);
    });
  });

  describe('Edge Cases - Real Code', () => {
    it('handles deeply nested structures', () => {
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

    it('handles single file at root', () => {
      const singleFile: FileNode = {
        name: 'file.txt',
        path: '/file.txt',
        type: 'file',
      };
      const expanded = new Set<string>();
      const { total } = countFolders(singleFile, expanded, false);
      expect(total).toBe(0); // Files don't count as folders
    });

    it('handles folder with no children', () => {
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
    it('maintains Set integrity through operations', () => {
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

    it('validates that Set.has() works correctly', () => {
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
    it('returns all collapsed folders when nothing is expanded', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      
      const collapsed = getAllCollapsedFolders(tree, expanded);
      // Should find src and public as collapsed at root level
      expect(collapsed).toContain('/project/src');
      expect(collapsed).toContain('/project/public');
    });

    it('returns only collapsed folders when some are expanded', () => {
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
    it('returns only direct collapsed children of target folder', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      const collapsed = getCollapsedWithinFolder('/project/src', tree, expanded);
      // Only direct children of src
      expect(collapsed).toContain('/project/src/components');
      expect(collapsed).toContain('/project/src/utils');
      expect(collapsed.length).toBe(2);
    });

    it('returns empty array for folders with no collapsed children', () => {
      const tree = createTestTree();
      const expanded = new Set<string>([
        '/project/src',
        '/project/src/components',
        '/project/src/utils',
      ]);
      
      const collapsed = getCollapsedWithinFolder('/project/src', tree, expanded);
      expect(collapsed).toEqual([]);
    });

    it('returns empty array when folder not found', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      const collapsed = getCollapsedWithinFolder('/nonexistent', tree, expanded);
      expect(collapsed).toEqual([]);
    });

    it('returns empty array when target is a file', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      const collapsed = getCollapsedWithinFolder('/project/src/components/App.tsx', tree, expanded);
      expect(collapsed).toEqual([]);
    });
  });

  describe('expandAllFolders - Global expand', () => {
    it('expands all collapsed folders in tree', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      
      expandAllFolders(tree, expanded);
      
      // All folders should be expanded
      expect(expanded.has('/project/src')).toBe(true);
      expect(expanded.has('/project/public')).toBe(true);
      expect(expanded.has('/project/src/components')).toBe(true);
      expect(expanded.has('/project/src/utils')).toBe(true);
    });

    it('handles partial expansion correctly', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      expandAllFolders(tree, expanded);
      
      // All should be expanded now
      const { total, expanded: expandedCount } = countFolders(tree, expanded, true);
      expect(expandedCount).toBe(total);
    });

    it('handles null tree gracefully', () => {
      const expanded = new Set<string>();
      
      expandAllFolders(null, expanded);
      
      expect(expanded.size).toBe(0);
    });

    it('handles undefined tree gracefully', () => {
      const expanded = new Set<string>();
      
      expandAllFolders(undefined, expanded);
      
      expect(expanded.size).toBe(0);
    });
  });

  describe('expandFoldersWithin - Scoped expand', () => {
    it('expands only collapsed folders within target folder', () => {
      const tree = createTestTree();
      const expanded = new Set<string>(['/project/src']);
      
      expandFoldersWithin('/project/src', tree, expanded);
      
      // src's children should be expanded
      expect(expanded.has('/project/src/components')).toBe(true);
      expect(expanded.has('/project/src/utils')).toBe(true);
      // But public should still be collapsed
      expect(expanded.has('/project/public')).toBe(false);
    });

    it('does not affect other branches', () => {
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
    it('collapses entire tree by clearing expanded set', () => {
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
    it('collapses only folders within target folder', () => {
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

    it('does not affect other branches', () => {
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
    it('workflow: expand entire tree from root', () => {
      const tree = createTestTree();
      const expanded = new Set<string>();
      
      // User right-clicks on root/empty space
      // Context type is 'empty', so use global expand
      expandAllFolders(tree, expanded);
      
      // All folders now expanded
      const { total, expanded: expandedCount } = countFolders(tree, expanded, true);
      expect(expandedCount).toBe(total);
    });

    it('workflow: expand only within src folder', () => {
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

    it('workflow: collapse entire tree from root', () => {
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

    it('workflow: collapse only within src folder', () => {
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
    it('getAllCollapsedFolders finds ALL collapsed folders including nested ones', () => {
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
});
