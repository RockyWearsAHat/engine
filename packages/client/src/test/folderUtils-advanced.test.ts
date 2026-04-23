/**
 * folderUtils-advanced.test.ts
 *
 * Behaviors: the AI controller navigates the project file tree to find files,
 * detect collapsed/expanded folder states, and determine which context menu
 * actions to show. These tests cover the lower-level node-based utilities.
 */
import { describe, expect, it } from 'vitest';
import type { FileNode } from '@engine/shared';
import {
  nodeHasCollapsedChildren,
  nodeHasExpandedChildren,
  hasFolderWithExpandedChildren,
  allChildrenExpanded,
  findParentByPath,
  hasSiblingFoldersCollapsed,
  hasSiblingFoldersExpanded,
} from '../components/FileTree/folderUtils.js';

// ─── Tree fixtures ────────────────────────────────────────────────────────────

const file = (path: string): FileNode => ({
  name: path.split('/').pop()!,
  path,
  type: 'file',
});

const dir = (path: string, children: FileNode[] = []): FileNode => ({
  name: path.split('/').pop()!,
  path,
  type: 'directory',
  children,
});

/**
 * /project
 *   src/         (can be collapsed or expanded)
 *     components/
 *       Button.tsx
 *     index.ts
 *   tests/
 *     unit.test.ts
 *   README.md
 */
const makeTree = () =>
  dir('/project', [
    dir('/project/src', [
      dir('/project/src/components', [file('/project/src/components/Button.tsx')]),
      file('/project/src/index.ts'),
    ]),
    dir('/project/tests', [file('/project/tests/unit.test.ts')]),
    file('/project/README.md'),
  ]);

// ─── nodeHasCollapsedChildren ─────────────────────────────────────────────────

describe('nodeHasCollapsedChildren', () => {
  it('returns true when a child directory is collapsed', () => {
    const node = makeTree();
    const expanded = new Set<string>(); // nothing expanded
    expect(nodeHasCollapsedChildren(node, expanded)).toBe(true);
  });

  it('returns false when all child directories are expanded', () => {
    const node = dir('/project', [
      dir('/project/src', []),
      dir('/project/tests', []),
    ]);
    const expanded = new Set(['/project/src', '/project/tests']);
    expect(nodeHasCollapsedChildren(node, expanded)).toBe(false);
  });

  it('returns false for a file node', () => {
    expect(nodeHasCollapsedChildren(file('/project/main.ts'), new Set())).toBe(false);
  });

  it('returns false for undefined', () => {
    expect(nodeHasCollapsedChildren(undefined, new Set())).toBe(false);
  });

  it('returns false for a directory with no children', () => {
    expect(nodeHasCollapsedChildren(dir('/empty'), new Set())).toBe(false);
  });

  it('detects collapsed folders at deeper nesting levels', () => {
    const node = dir('/project', [
      dir('/project/src', [
        dir('/project/src/deep', []),   // not expanded
      ]),
    ]);
    const expanded = new Set(['/project/src']); // src is open, deep is not
    expect(nodeHasCollapsedChildren(node, expanded)).toBe(true);
  });
});

// ─── nodeHasExpandedChildren ──────────────────────────────────────────────────

describe('nodeHasExpandedChildren', () => {
  it('returns true when at least one child directory is expanded', () => {
    const node = makeTree();
    const expanded = new Set(['/project/src']);
    expect(nodeHasExpandedChildren(node, expanded)).toBe(true);
  });

  it('returns false when no child directories are expanded', () => {
    const node = makeTree();
    expect(nodeHasExpandedChildren(node, new Set())).toBe(false);
  });

  it('returns false for a file node', () => {
    expect(nodeHasExpandedChildren(file('/project/main.ts'), new Set(['/project/main.ts']))).toBe(false);
  });

  it('returns false for undefined', () => {
    expect(nodeHasExpandedChildren(undefined, new Set())).toBe(false);
  });

  it('detects expanded folders nested deeply', () => {
    const node = dir('/project', [
      dir('/project/src', [
        dir('/project/src/deep', []),
      ]),
    ]);
    const expanded = new Set(['/project/src/deep']);
    expect(nodeHasExpandedChildren(node, expanded)).toBe(true);
  });
});

// ─── hasFolderWithExpandedChildren ───────────────────────────────────────────

describe('hasFolderWithExpandedChildren', () => {
  it('returns true when the target folder has an expanded descendant', () => {
    const tree = makeTree();
    const expanded = new Set(['/project/src/components']);
    expect(hasFolderWithExpandedChildren('/project/src', tree, expanded)).toBe(true);
  });

  it('returns false when no children of the target folder are expanded', () => {
    const tree = makeTree();
    expect(hasFolderWithExpandedChildren('/project/src', tree, new Set())).toBe(false);
  });

  it('returns false when the target path is a file', () => {
    const tree = makeTree();
    expect(hasFolderWithExpandedChildren('/project/README.md', tree, new Set())).toBe(false);
  });

  it('returns false when the target path does not exist in the tree', () => {
    const tree = makeTree();
    expect(hasFolderWithExpandedChildren('/nonexistent', tree, new Set())).toBe(false);
  });

  it('returns false for null tree', () => {
    expect(hasFolderWithExpandedChildren('/project', null, new Set())).toBe(false);
  });
});

// ─── allChildrenExpanded ──────────────────────────────────────────────────────

describe('allChildrenExpanded', () => {
  it('returns true when all immediate directory children are expanded', () => {
    const tree = dir('/project', [
      dir('/project/src', []),
      dir('/project/tests', []),
      file('/project/README.md'),
    ]);
    const expanded = new Set(['/project/src', '/project/tests']);
    expect(allChildrenExpanded('/project', tree, expanded)).toBe(true);
  });

  it('returns false when at least one directory child is collapsed', () => {
    const tree = dir('/project', [
      dir('/project/src', []),
      dir('/project/tests', []),
    ]);
    const expanded = new Set(['/project/src']); // tests not expanded
    expect(allChildrenExpanded('/project', tree, expanded)).toBe(false);
  });

  it('returns true for a folder with no children', () => {
    const tree = dir('/project', [dir('/project/empty', [])]);
    expect(allChildrenExpanded('/project/empty', tree, new Set())).toBe(true);
  });

  it('returns false when the target path is a file', () => {
    const tree = makeTree();
    expect(allChildrenExpanded('/project/README.md', tree, new Set())).toBe(false);
  });

  it('ignores file children when checking expansion', () => {
    const tree = dir('/project', [
      dir('/project/src', []),
      file('/project/main.ts'),
    ]);
    const expanded = new Set(['/project/src']);
    expect(allChildrenExpanded('/project', tree, expanded)).toBe(true);
  });
});

// ─── findParentByPath ─────────────────────────────────────────────────────────

describe('findParentByPath', () => {
  it('finds the parent directory of a file', () => {
    const tree = makeTree();
    const parent = findParentByPath('/project/src/index.ts', tree);
    expect(parent?.path).toBe('/project/src');
  });

  it('finds the parent directory of a nested file', () => {
    const tree = makeTree();
    const parent = findParentByPath('/project/src/components/Button.tsx', tree);
    expect(parent?.path).toBe('/project/src/components');
  });

  it('returns the root when the path has no parent segment', () => {
    const tree = makeTree();
    const parent = findParentByPath('/project', tree);
    expect(parent?.path).toBe('/project');
  });

  it('returns undefined for null tree', () => {
    expect(findParentByPath('/project/src', null)).toBeUndefined();
  });

  it('returns undefined for empty childPath', () => {
    expect(findParentByPath('', makeTree())).toBeUndefined();
  });
});

// ─── hasSiblingFoldersCollapsed ───────────────────────────────────────────────

describe('hasSiblingFoldersCollapsed', () => {
  it('returns true when a sibling directory of a file is collapsed', () => {
    const tree = makeTree();
    const expanded = new Set<string>(); // src and tests are both collapsed
    // README.md is in /project, which has src/ and tests/ as siblings
    expect(hasSiblingFoldersCollapsed('/project/README.md', tree, expanded)).toBe(true);
  });

  it('returns false when all sibling directories are expanded', () => {
    const tree = dir('/project', [
      dir('/project/src', []),
      dir('/project/tests', []),
      file('/project/README.md'),
    ]);
    const expanded = new Set(['/project/src', '/project/tests']);
    expect(hasSiblingFoldersCollapsed('/project/README.md', tree, expanded)).toBe(false);
  });

  it('returns false for a path with no parent in the tree', () => {
    expect(hasSiblingFoldersCollapsed('/ghost/file.ts', makeTree(), new Set())).toBe(false);
  });
});

// ─── hasSiblingFoldersExpanded ────────────────────────────────────────────────

describe('hasSiblingFoldersExpanded', () => {
  it('returns true when a sibling directory of a file is expanded', () => {
    const tree = makeTree();
    const expanded = new Set(['/project/src']);
    expect(hasSiblingFoldersExpanded('/project/README.md', tree, expanded)).toBe(true);
  });

  it('returns false when no sibling directories are expanded', () => {
    const tree = makeTree();
    expect(hasSiblingFoldersExpanded('/project/README.md', tree, new Set())).toBe(false);
  });

  it('returns false for a path with no parent in the tree', () => {
    expect(hasSiblingFoldersExpanded('/ghost/file.ts', makeTree(), new Set())).toBe(false);
  });
});
