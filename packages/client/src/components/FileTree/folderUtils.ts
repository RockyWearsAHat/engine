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
  node: FileNode | null | undefined,
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
 * Check if a node directly has any collapsed children (without tree search)
 * @param node - The node to check
 * @param expandedFolders - Set of expanded folder paths
 */
export const nodeHasCollapsedChildren = (
  node: FileNode | undefined,
  expandedFolders: Set<string>
): boolean => {
  if (!node || node.type === 'file') return false;

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

  return node.children ? node.children.some(hasCollapsedChild) : false;
};

/**
 * Check if a node directly has any expanded children (without tree search)
 * @param node - The node to check
 * @param expandedFolders - Set of expanded folder paths
 */
export const nodeHasExpandedChildren = (
  node: FileNode | undefined,
  expandedFolders: Set<string>
): boolean => {
  if (!node || node.type === 'file') return false;

  // Recursively check if any child is an expanded folder
  const hasExpandedChild = (n: FileNode): boolean => {
    if (n.type === 'directory' && expandedFolders.has(n.path)) {
      return true; // Found an expanded folder
    }
    if (n.children) {
      return n.children.some(hasExpandedChild);
    }
    return false;
  };

  return node.children ? node.children.some(hasExpandedChild) : false;
};

/**
 * Check if a folder has any expanded children that could be collapsed
 * @param folderPath - Path of the folder to check
 * @param node - The tree node to search in
 * @param expandedFolders - Set of expanded folder paths
 */
export const hasFolderWithExpandedChildren = (
  folderPath: string,
  node: FileNode | null | undefined,
  expandedFolders: Set<string>
): boolean => {
  if (!node) return false;

  const targetFolder = findNodeByPath(folderPath, node);
  if (!targetFolder || targetFolder.type === 'file') return false;

  // Recursively check if any child is an expanded folder
  const hasExpandedChild = (n: FileNode): boolean => {
    if (n.type === 'directory' && expandedFolders.has(n.path)) {
      return true; // Found an expanded folder
    }
    if (n.children) {
      return n.children.some(hasExpandedChild);
    }
    return false;
  };

  return targetFolder.children ? targetFolder.children.some(hasExpandedChild) : false;
};

/**
 * Check if all children of a folder are already expanded
 * @param folderPath - Path of the folder to check
 * @param node - The tree node to search in
 * @param expandedFolders - Set of expanded folder paths
 */
export const allChildrenExpanded = (
  folderPath: string,
  node: FileNode | null | undefined,
  expandedFolders: Set<string>
): boolean => {
  if (!node) return false;

  const targetFolder = findNodeByPath(folderPath, node);
  if (!targetFolder || targetFolder.type === 'file') return false;

  // If no children, nothing to expand
  if (!targetFolder.children || targetFolder.children.length === 0) return true;

  // Check if all folder children are expanded
  const allExpanded = targetFolder.children.every(child => {
    if (child.type === 'file') return true; // Files don't need to be expanded
    return expandedFolders.has(child.path);
  });

  return allExpanded;
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
 * Find parent folder node by path
 * @param childPath - Path of the child (file or folder)
 * @param tree - The file tree
 */
export const findParentByPath = (
  childPath: string,
  tree: FileNode | null | undefined
): FileNode | undefined => {
  if (!tree || !childPath) return undefined;
  
  const parentPath = childPath.substring(0, childPath.lastIndexOf('/'));
  if (!parentPath) return tree; // Root is the parent
  return findNodeByPath(parentPath, tree);
};

/**
 * Check if any sibling folders are collapsed (used for file right-clicks)
 * @param filePath - Path of the file being right-clicked
 * @param tree - The file tree
 * @param expandedFolders - Set of expanded folder paths
 */
export const hasSiblingFoldersCollapsed = (
  filePath: string,
  tree: FileNode | null | undefined,
  expandedFolders: Set<string>
): boolean => {
  const parent = findParentByPath(filePath, tree);
  if (!parent || !parent.children) return false;
  
  for (const child of parent.children) {
    if (child.type === 'directory' && !expandedFolders.has(child.path)) {
      return true;
    }
  }
  return false;
};

/**
 * Check if any sibling folders are expanded (used for file right-clicks)
 * @param filePath - Path of the file being right-clicked
 * @param tree - The file tree
 * @param expandedFolders - Set of expanded folder paths
 */
export const hasSiblingFoldersExpanded = (
  filePath: string,
  tree: FileNode | null | undefined,
  expandedFolders: Set<string>
): boolean => {
  const parent = findParentByPath(filePath, tree);
  if (!parent || !parent.children) return false;
  
  for (const child of parent.children) {
    if (child.type === 'directory' && expandedFolders.has(child.path)) {
      return true;
    }
  }
  return false;
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

/**
 * Get all collapsed folders in the entire tree (for global expand all)
 * Returns ALL collapsed folders regardless of nesting depth
 * @param node - The root node to search from
 * @param expandedFolders - Set of expanded folder paths
 * @param isRoot - Whether this is the root call
 */
export const getAllCollapsedFolders = (
  node: FileNode,
  expandedFolders: Set<string>,
  isRoot: boolean = true
): string[] => {
  if (node.type === 'file') return [];
  const collapsed: string[] = [];

  // Add this folder as collapsed if not root and not expanded
  if (!isRoot && !expandedFolders.has(node.path)) {
    collapsed.push(node.path);
    // STILL recurse into this collapsed folder to find nested collapsed folders
    // This allows us to return ALL collapsed folders for global expand
  }

  // Recurse into children (whether this folder is expanded or not)
  if (node.children) {
    for (const child of node.children) {
      collapsed.push(...getAllCollapsedFolders(child, expandedFolders, false));
    }
  }
  return collapsed;
};

/**
 * Get collapsed folders only within a specific target folder
 * @param folderPath - Path of the target folder
 * @param node - The tree node to search in
 * @param expandedFolders - Set of expanded folder paths
 */
export const getCollapsedWithinFolder = (
  folderPath: string,
  node: FileNode | undefined,
  expandedFolders: Set<string>
): string[] => {
  if (!node) return [];

  const targetFolder = findNodeByPath(folderPath, node);
  if (!targetFolder || targetFolder.type === 'file' || !targetFolder.children) {
    return [];
  }

  const collapsed: string[] = [];
  for (const child of targetFolder.children) {
    if (child.type === 'directory' && !expandedFolders.has(child.path)) {
      collapsed.push(child.path);
    }
  }
  return collapsed;
};

/**
 * Expand all folders in the entire tree (global expand all)
 * @param tree - The file tree
 * @param expandedFolders - Set of expanded folder paths (will be modified)
 */
export const expandAllFolders = (
  tree: FileNode | null | undefined,
  expandedFolders: Set<string>
): void => {
  if (!tree) return;
  const collapsed = getAllCollapsedFolders(tree, expandedFolders, true);
  collapsed.forEach(path => expandedFolders.add(path));
};

/**
 * Expand folders only within a specific target folder (scoped expand)
 * @param folderPath - Path of the target folder
 * @param tree - The file tree
 * @param expandedFolders - Set of expanded folder paths (will be modified)
 */
export const expandFoldersWithin = (
  folderPath: string,
  tree: FileNode | null | undefined,
  expandedFolders: Set<string>
): void => {
  const collapsed = getCollapsedWithinFolder(folderPath, tree as FileNode | undefined, expandedFolders);
  collapsed.forEach(path => expandedFolders.add(path));
};

/**
 * Collapse all folders in the entire tree (global collapse all)
 * @param expandedFolders - Set of expanded folder paths (will be cleared)
 */
export const collapseAllFolders = (expandedFolders: Set<string>): void => {
  expandedFolders.clear();
};

/**
 * Collapse folders only within a specific target folder (scoped collapse)
 * @param folderPath - Path of the target folder
 * @param expandedFolders - Set of expanded folder paths (will be modified)
 */
export const collapseFoldersWithin = (
  folderPath: string,
  expandedFolders: Set<string>
): void => {
  // Remove all folders that are within this path
  const toRemove = Array.from(expandedFolders).filter(
    path => path.startsWith(folderPath + '/') && path !== folderPath
  );
  toRemove.forEach(path => expandedFolders.delete(path));
};
