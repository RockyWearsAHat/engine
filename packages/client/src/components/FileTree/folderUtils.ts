/**
 * Pure utility functions for folder tree operations
 * Exported for testing and reuse
 */

import type { FileNode } from '@engine/shared';

/**
 * Count total folders and expanded folders in a tree
 * @param node - The root node to count from
 * @param expandedFolders - Set of expanded folder paths
 * @param isRoot - Whether this is the root call (skips counting root itself)
 */
export const countFolders = (
  node: FileNode,
  expandedFolders: Set<string>,
  isRoot: boolean = false
): { total: number; expanded: number } => {
  if (node.type === 'file') return { total: 0, expanded: 0 };

  let total = isRoot ? 0 : 1; // Don't count the root folder itself
  let expanded = isRoot ? 0 : (expandedFolders.has(node.path) ? 1 : 0);

  if (node.children) {
    for (const child of node.children) {
      const count = countFolders(child, expandedFolders, false);
      total += count.total;
      expanded += count.expanded;
    }
  }
  return { total, expanded };
};

/**
 * Check if a folder has any collapsed children
 * @param folderPath - Path of the folder to check
 * @param node - The tree node to search in
 * @param expandedFolders - Set of expanded folder paths
 */
export const hasFolderWithCollapsedChildren = (
  folderPath: string,
  node: FileNode | undefined,
  expandedFolders: Set<string>
): boolean => {
  if (!node) return false;

  const targetFolder = findNodeByPath(folderPath, node);
  if (!targetFolder || targetFolder.type === 'file') return false;

  // Recursively check if any child is a collapsed folder
  const hasCollapsedChild = (n: FileNode): boolean => {
    if (n.type === 'directory' && !expandedFolders.has(n.path)) {
      return true; // Found a collapsed folder
    }
    if (n.children) {
      return n.children.some(hasCollapsedChild);
    }
    return false;
  };

  return targetFolder.children ? targetFolder.children.some(hasCollapsedChild) : false;
};

/**
 * Find a node by its path in the tree
 * @param path - The path to search for
 * @param node - The root node to search from
 */
export const findNodeByPath = (
  path: string,
  node: FileNode | null | undefined
): FileNode | undefined => {
  if (!node) return undefined;
  if (node.path === path) return node;

  if (node.children) {
    for (const child of node.children) {
      const found = findNodeByPath(path, child);
      if (found) return found;
    }
  }

  return undefined;
};

/**
 * Determine if Expand All menu item should be shown
 * @param folderPath - Path of the folder being right-clicked
 * @param tree - The file tree
 * @param expandedFolders - Set of expanded folder paths
 */
export const shouldShowExpandAll = (
  folderPath: string,
  tree: FileNode | null | undefined,
  expandedFolders: Set<string>
): boolean => {
  return hasFolderWithCollapsedChildren(folderPath, tree || undefined, expandedFolders);
};

/**
 * Determine if Collapse All menu item should be shown
 * @param expandedFolders - Set of expanded folder paths
 */
export const shouldShowCollapseAll = (expandedFolders: Set<string>): boolean => {
  return expandedFolders.size > 0;
};
