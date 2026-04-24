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
  getAllCollapsedFolders,
  getCollapsedWithinFolder,
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
  it('ChildDirCollapsed_ReturnsTrue', () => {
    const node = makeTree();
    const expanded = new Set<string>(); // nothing expanded
    expect(nodeHasCollapsedChildren(node, expanded)).toBe(true);
  });

  it('AllChildDirsExpanded_ReturnsFalse', () => {
    const node = dir('/project', [
      dir('/project/src', []),
      dir('/project/tests', []),
    ]);
    const expanded = new Set(['/project/src', '/project/tests']);
    expect(nodeHasCollapsedChildren(node, expanded)).toBe(false);
  });

  it('NodeHasCollapsedChildren_FileNode_ReturnsFalse', () => {
    expect(nodeHasCollapsedChildren(file('/project/main.ts'), new Set())).toBe(false);
  });

  it('NodeHasCollapsedChildren_Undefined_ReturnsFalse', () => {
    expect(nodeHasCollapsedChildren(undefined, new Set())).toBe(false);
  });

  it('DirNoChildren_ReturnsFalse', () => {
    expect(nodeHasCollapsedChildren(dir('/empty'), new Set())).toBe(false);
  });

  it('DeeperNesting_CollapsedDetected', () => {
    const node = dir('/project', [
      dir('/project/src', [
        dir('/project/src/deep', []),   // not expanded
      ]),
    ]);
    const expanded = new Set(['/project/src']); // src is open, deep is not
    expect(nodeHasCollapsedChildren(node, expanded)).toBe(true);
  });

  it('OnlyFileChildren_ReturnsFalse', () => {
    const node = dir('/project', [
      file('/project/README.md'),
      file('/project/main.ts'),
    ]);
    expect(nodeHasCollapsedChildren(node, new Set())).toBe(false);
  });
});

// ─── nodeHasExpandedChildren ──────────────────────────────────────────────────

describe('nodeHasExpandedChildren', () => {
  it('AtLeastOneChildExpanded_ReturnsTrue', () => {
    const node = makeTree();
    const expanded = new Set(['/project/src']);
    expect(nodeHasExpandedChildren(node, expanded)).toBe(true);
  });

  it('NoChildDirsExpanded_ReturnsFalse', () => {
    const node = makeTree();
    expect(nodeHasExpandedChildren(node, new Set())).toBe(false);
  });

  it('NodeHasExpandedChildren_FileNode_ReturnsFalse', () => {
    expect(nodeHasExpandedChildren(file('/project/main.ts'), new Set(['/project/main.ts']))).toBe(false);
  });

  it('NodeHasExpandedChildren_Undefined_ReturnsFalse', () => {
    expect(nodeHasExpandedChildren(undefined, new Set())).toBe(false);
  });

  it('DeeplyNestedExpanded_Detected', () => {
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
  it('TargetHasExpandedDescendant_ReturnsTrue', () => {
    const tree = makeTree();
    const expanded = new Set(['/project/src/components']);
    expect(hasFolderWithExpandedChildren('/project/src', tree, expanded)).toBe(true);
  });

  it('NoChildrenExpanded_ReturnsFalse', () => {
    const tree = makeTree();
    expect(hasFolderWithExpandedChildren('/project/src', tree, new Set())).toBe(false);
  });

  it('HasFolderWithExpandedChildren_TargetIsFile_ReturnsFalse', () => {
    const tree = makeTree();
    expect(hasFolderWithExpandedChildren('/project/README.md', tree, new Set())).toBe(false);
  });

  it('TargetPathNotInTree_ReturnsFalse', () => {
    const tree = makeTree();
    expect(hasFolderWithExpandedChildren('/nonexistent', tree, new Set())).toBe(false);
  });

  it('NullTree_ReturnsFalse', () => {
    expect(hasFolderWithExpandedChildren('/project', null, new Set())).toBe(false);
  });
});

// ─── allChildrenExpanded ──────────────────────────────────────────────────────

describe('allChildrenExpanded', () => {
  it('AllImmediateDirChildrenExpanded_ReturnsTrue', () => {
    const tree = dir('/project', [
      dir('/project/src', []),
      dir('/project/tests', []),
      file('/project/README.md'),
    ]);
    const expanded = new Set(['/project/src', '/project/tests']);
    expect(allChildrenExpanded('/project', tree, expanded)).toBe(true);
  });

  it('AtLeastOneDirChildCollapsed_ReturnsFalse', () => {
    const tree = dir('/project', [
      dir('/project/src', []),
      dir('/project/tests', []),
    ]);
    const expanded = new Set(['/project/src']); // tests not expanded
    expect(allChildrenExpanded('/project', tree, expanded)).toBe(false);
  });

  it('FolderNoChildren_ReturnsTrue', () => {
    const tree = dir('/project', [dir('/project/empty', [])]);
    expect(allChildrenExpanded('/project/empty', tree, new Set())).toBe(true);
  });

  it('AllChildrenExpanded_TargetIsFile_ReturnsFalse', () => {
    const tree = makeTree();
    expect(allChildrenExpanded('/project/README.md', tree, new Set())).toBe(false);
  });

  it('FileChildrenIgnored_CheckExpansion', () => {
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
  it('FileParentDir_Found', () => {
    const tree = makeTree();
    const parent = findParentByPath('/project/src/index.ts', tree);
    expect(parent?.path).toBe('/project/src');
  });

  it('NestedFileParentDir_Found', () => {
    const tree = makeTree();
    const parent = findParentByPath('/project/src/components/Button.tsx', tree);
    expect(parent?.path).toBe('/project/src/components');
  });

  it('PathNoParentSegment_RootReturned', () => {
    const tree = makeTree();
    const parent = findParentByPath('/project', tree);
    expect(parent?.path).toBe('/project');
  });

  it('NullTreeFindParent_Undefined', () => {
    expect(findParentByPath('/project/src', null)).toBeUndefined();
  });

  it('EmptyChildPath_Undefined', () => {
    expect(findParentByPath('', makeTree())).toBeUndefined();
  });
});

// ─── hasSiblingFoldersCollapsed ───────────────────────────────────────────────

describe('hasSiblingFoldersCollapsed', () => {
  it('SiblingDirCollapsed_ReturnsTrue', () => {
    const tree = makeTree();
    const expanded = new Set<string>(); // src and tests are both collapsed
    // README.md is in /project, which has src/ and tests/ as siblings
    expect(hasSiblingFoldersCollapsed('/project/README.md', tree, expanded)).toBe(true);
  });

  it('AllSiblingDirsExpanded_ReturnsFalse', () => {
    const tree = dir('/project', [
      dir('/project/src', []),
      dir('/project/tests', []),
      file('/project/README.md'),
    ]);
    const expanded = new Set(['/project/src', '/project/tests']);
    expect(hasSiblingFoldersCollapsed('/project/README.md', tree, expanded)).toBe(false);
  });

  it('HasSiblingFoldersCollapsed_NoParentInTree_ReturnsFalse', () => {
    expect(hasSiblingFoldersCollapsed('/ghost/file.ts', makeTree(), new Set())).toBe(false);
  });
});

// ─── hasSiblingFoldersExpanded ────────────────────────────────────────────────

describe('hasSiblingFoldersExpanded', () => {
  it('SiblingDirExpanded_ReturnsTrue', () => {
    const tree = makeTree();
    const expanded = new Set(['/project/src']);
    expect(hasSiblingFoldersExpanded('/project/README.md', tree, expanded)).toBe(true);
  });

  it('NoSiblingDirsExpanded_ReturnsFalse', () => {
    const tree = makeTree();
    expect(hasSiblingFoldersExpanded('/project/README.md', tree, new Set())).toBe(false);
  });

  it('HasSiblingFoldersExpanded_NoParentInTree_ReturnsFalse', () => {
    expect(hasSiblingFoldersExpanded('/ghost/file.ts', makeTree(), new Set())).toBe(false);
  });
});

// ─── hasFolderWithExpandedChildren — no children branch ──────────────────────

describe('hasFolderWithExpandedChildren — folder with no children array', () => {
  it('hasFolderWithExpandedChildren_DirWithNoChildren_ReturnsFalse', () => {
    const emptyDir: FileNode = { name: 'proj', path: '/proj', type: 'directory' };
    expect(hasFolderWithExpandedChildren('/proj', emptyDir, new Set(['/proj']))).toBe(false);
  });
});
describe('allChildrenExpanded — folder with no children property', () => {
  it('allChildrenExpanded_FolderWithNoChildrenProp_ReturnsTrue', () => {
    const tree: FileNode = {
      name: 'project',
      path: '/project',
      type: 'directory',
      children: [
        { name: 'empty', path: '/project/empty', type: 'directory' },
      ],
    };
    expect(allChildrenExpanded('/project/empty', tree, new Set())).toBe(true);
  });
});

describe('nodeHasCollapsedChildren — folder with no children array', () => {
  it('nodeHasCollapsedChildren_DirWithNoChildrenProp_ReturnsFalse', () => {
    const emptyDir: FileNode = { name: 'src', path: '/project/src', type: 'directory' };
    expect(nodeHasCollapsedChildren(emptyDir, new Set())).toBe(false);
  });
});

describe('nodeHasExpandedChildren — folder with no children array', () => {
  it('nodeHasExpandedChildren_DirWithNoChildrenProp_ReturnsFalse', () => {
    const emptyDir: FileNode = { name: 'src', path: '/project/src', type: 'directory' };
    expect(nodeHasExpandedChildren(emptyDir, new Set(['/project/src']))).toBe(false);
  });
});
// ─── getAllCollapsedFolders — directory with no children ──────────────────────

describe('getAllCollapsedFolders — directory with no children array', () => {
  it('getAllCollapsedFolders_DirWithNoChildrenProp_ReturnsOwnPathOnly', () => {
    const emptyDir: FileNode = { name: 'src', path: '/project/src', type: 'directory' };
    const result = getAllCollapsedFolders(emptyDir, new Set(), false);
    expect(result).toEqual(['/project/src']);
  });
});

// ─── getCollapsedWithinFolder — undefined node ────────────────────────────────

describe('getCollapsedWithinFolder — undefined node', () => {
  it('getCollapsedWithinFolder_UndefinedNode_ReturnsEmpty', () => {
    expect(getCollapsedWithinFolder('/any', undefined, new Set())).toEqual([]);
  });
});

describe('allChildrenExpanded — null node', () => {
  it('allChildrenExpanded_NullNode_ReturnsFalse', () => {
    expect(allChildrenExpanded('/project', null, new Set())).toBe(false);
  });
});
