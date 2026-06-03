import { useEffect, useMemo, useState } from 'react';
import { ChevronRight, FileText, Folder, FolderOpen } from 'lucide-react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { REVEAL_FILE_LOCATION_EVENT, type RevealFileLocationDetail } from '../../editorEvents.js';

type QualityIssue = NonNullable<ReturnType<typeof useStore.getState>['qualityReport']>['issues'][number];

type QualityFileNode = {
  name: string;
  path: string;
  key: string;
  issues: QualityIssue[];
  groups: QualityIssueGroup[];
};

type QualityIssueGroup = {
  key: string;
  severity: QualityIssue['severity'];
  category: string;
  issues: QualityIssue[];
  representativeMessage: string;
  representativeSuggestion: string | null;
};

type QualityDirectoryNode = {
  name: string;
  path: string;
  key: string;
  directories: Map<string, QualityDirectoryNode>;
  files: QualityFileNode[];
};

function severityRank(severity: string): number {
  switch (severity) {
    case 'high':
      return 0;
    case 'medium':
      return 1;
    default:
      return 2;
  }
}

function sortIssues(issues: QualityIssue[]): QualityIssue[] {
  return [...issues].sort((a, b) => {
    if (severityRank(a.severity) !== severityRank(b.severity)) {
      return severityRank(a.severity) - severityRank(b.severity);
    }
    return a.line - b.line;
  });
}

function mostCommonValue(values: Array<string | null | undefined>): string | null {
  const counts = new Map<string, number>();
  for (const value of values) {
    if (!value) {
      continue;
    }
    counts.set(value, (counts.get(value) ?? 0) + 1);
  }
  let best: string | null = null;
  let bestCount = -1;
  for (const [value, count] of counts.entries()) {
    if (count > bestCount) {
      best = value;
      bestCount = count;
    }
  }
  return best;
}

function buildIssueGroups(filePath: string, issues: QualityIssue[]): QualityIssueGroup[] {
  const grouped = new Map<string, QualityIssue[]>();
  for (const issue of issues) {
    const key = `${issue.severity}::${issue.category}`;
    const existing = grouped.get(key) ?? [];
    existing.push(issue);
    grouped.set(key, existing);
  }

  const groups = Array.from(grouped.entries()).map(([groupKey, groupedIssues]) => {
    const sorted = sortIssues(groupedIssues);
    const representativeMessage = mostCommonValue(sorted.map((issue) => issue.message)) ?? sorted[0]?.message ?? 'Issue';
    const representativeSuggestion = mostCommonValue(sorted.map((issue) => issue.suggestion ?? null));
    const [severity, category] = groupKey.split('::');
    return {
      key: `group:${normalizeIssuePath(filePath)}:${groupKey}`,
      severity: (severity as QualityIssue['severity']) ?? 'low',
      category: category ?? 'quality',
      issues: sorted,
      representativeMessage,
      representativeSuggestion,
    };
  });

  groups.sort((a, b) => {
    if (severityRank(a.severity) !== severityRank(b.severity)) {
      return severityRank(a.severity) - severityRank(b.severity);
    }
    return a.category.localeCompare(b.category);
  });

  return groups;
}

function buildIssueTree(entries: Array<readonly [string, QualityIssue[]]>): QualityDirectoryNode {
  const root: QualityDirectoryNode = {
    name: '(root)',
    path: '',
    key: 'dir:/',
    directories: new Map(),
    files: [],
  };

  for (const [filePath, issues] of entries) {
    const cleanPath = filePath.replace(/^\/+/, '');
    const parts = cleanPath.split('/').filter(Boolean);
    if (parts.length === 0) {
      continue;
    }

    const fileName = parts[parts.length - 1] ?? filePath;
    const directories = parts.slice(0, -1);
    let current = root;
    let currentPath = '';

    for (const segment of directories) {
      currentPath = currentPath ? `${currentPath}/${segment}` : segment;
      let child = current.directories.get(segment);
      if (!child) {
        child = {
          name: segment,
          path: currentPath,
          key: `dir:${currentPath}`,
          directories: new Map(),
          files: [],
        };
        current.directories.set(segment, child);
      }
      current = child;
    }

    current.files.push({
      name: fileName,
      path: cleanPath,
      key: `file:${cleanPath}`,
      issues,
      groups: buildIssueGroups(cleanPath, issues),
    });
  }

  return root;
}

function normalizeIssuePath(path: string): string {
  return path.replace(/^\/+/, '');
}

function issueMatchesActiveFile(issuePath: string, activePath: string | null): boolean {
  if (!activePath) {
    return false;
  }
  const normalizedIssuePath = normalizeIssuePath(issuePath);
  const normalizedActivePath = normalizeIssuePath(activePath);
  return normalizedActivePath === normalizedIssuePath || normalizedActivePath.endsWith(`/${normalizedIssuePath}`);
}

function shouldHideAsLowDuplicateNoise(group: QualityIssueGroup, showLowDuplicateNoise: boolean): boolean {
  if (showLowDuplicateNoise) {
    return false;
  }
  return group.category === 'duplicate-content' && group.severity === 'low';
}

export default function QualityPanel() {
  const {
    qualityReport,
    qualityLoading,
    qualityError,
    qualityProgress,
    openFiles,
    activeFilePath,
    setActiveFile,
  } = useStore();

  const groupedIssues = useMemo(() => {
    const groups = new Map<string, QualityIssue[]>();
    if (!qualityReport) {
      return [];
    }
    for (const issue of qualityReport.issues) {
      const group = groups.get(issue.file) ?? [];
      group.push(issue);
      groups.set(issue.file, group);
    }
    return Array.from(groups.entries())
      .map(([file, issues]) => [file, sortIssues(issues)] as const)
      .sort((a, b) => a[0].localeCompare(b[0]));
  }, [qualityReport]);

  const qualityIssueTree = useMemo(() => buildIssueTree(groupedIssues), [groupedIssues]);

  const progressPercent = Math.max(0, Math.min(100, Math.round(qualityProgress?.percent ?? 0)));
  const hasLoadedReport = qualityReport !== null;
  const hasStartedScan = qualityLoading || qualityProgress !== null;
  const likelyStaleReport = Boolean(qualityReport && qualityReport.issueCount > 5000);
  const progressText = qualityProgress
    ? `Scanning project - ${qualityProgress.currentFile || 'project'}|${qualityProgress.currentFunction || 'scan'}(//${qualityProgress.section || qualityProgress.phase})`
    : hasStartedScan
      ? 'Scanning project - project|scan(//start)'
      : 'Waiting for automatic project scan...';

  const issuesByFile = useMemo(() => {
    const counts = new Map<string, number>();
    if (!qualityReport) {
      return counts;
    }
    for (const issue of qualityReport.issues) {
      counts.set(issue.file, (counts.get(issue.file) ?? 0) + 1);
    }
    return counts;
  }, [qualityReport]);

  const sortedOpenEditors = useMemo(() => {
    const files = [...openFiles];
    files.sort((a, b) => {
      const aActive = a.path === activeFilePath ? 0 : 1;
      const bActive = b.path === activeFilePath ? 0 : 1;
      if (aActive !== bActive) {
        return aActive - bActive;
      }
      return a.path.localeCompare(b.path);
    });
    return files;
  }, [activeFilePath, openFiles]);

  const progressLabel = qualityLoading ? `Scanning ${progressPercent}%` : 'Scan complete';
  const [expandedNodes, setExpandedNodes] = useState<Record<string, boolean>>({});
  const [showLowDuplicateNoise, setShowLowDuplicateNoise] = useState(false);

  const visibleGroupCount = useMemo(() => {
    let count = 0;
    for (const [, issues] of groupedIssues) {
      const groups = buildIssueGroups('', issues);
      for (const group of groups) {
        if (!shouldHideAsLowDuplicateNoise(group, showLowDuplicateNoise)) {
          count += 1;
        }
      }
    }
    return count;
  }, [groupedIssues, showLowDuplicateNoise]);

  const hiddenLowDuplicateIssueCount = useMemo(() => {
    if (showLowDuplicateNoise || !qualityReport) {
      return 0;
    }
    let hidden = 0;
    for (const issue of qualityReport.issues) {
      if (issue.category === 'duplicate-content' && issue.severity === 'low') {
        hidden += 1;
      }
    }
    return hidden;
  }, [qualityReport, showLowDuplicateNoise]);

  useEffect(() => {
    const defaults: Record<string, boolean> = {
      'dir:/': true,
    };

    groupedIssues.slice(0, 3).forEach(([file]) => {
      const normalized = normalizeIssuePath(file);
      const parts = normalized.split('/').filter(Boolean);
      let currentPath = '';
      for (const segment of parts.slice(0, -1)) {
        currentPath = currentPath ? `${currentPath}/${segment}` : segment;
        defaults[`dir:${currentPath}`] = true;
      }
      defaults[`file:${normalized}`] = true;
    });

    const activeMatch = groupedIssues.find(([file]) => issueMatchesActiveFile(file, activeFilePath));
    if (activeMatch) {
      const normalized = normalizeIssuePath(activeMatch[0]);
      const parts = normalized.split('/').filter(Boolean);
      let currentPath = '';
      for (const segment of parts.slice(0, -1)) {
        currentPath = currentPath ? `${currentPath}/${segment}` : segment;
        defaults[`dir:${currentPath}`] = true;
      }
      defaults[`file:${normalized}`] = true;
    }

    setExpandedNodes((current) => {
      if (Object.keys(current).length === 0) {
        return defaults;
      }

      const next: Record<string, boolean> = { ...current };
      for (const [key, value] of Object.entries(defaults)) {
        if (typeof next[key] === 'undefined') {
          next[key] = value;
        }
      }
      return next;
    });
  }, [activeFilePath, groupedIssues]);

  const resolveIssuePath = (issuePath: string) => {
    if (issuePath.startsWith('/')) {
      return issuePath;
    }
    const projectPath = qualityReport?.projectPath ?? '';
    if (!projectPath) {
      return issuePath;
    }
    return `${projectPath.replace(/\/$/, '')}/${issuePath.replace(/^\//, '')}`;
  };

  const openIssueInEditor = (issuePath: string, line: number) => {
    const resolvedPath = resolveIssuePath(issuePath);
    setActiveFile(resolvedPath);
    wsClient.send({ type: 'file.read', path: resolvedPath });
    const detail: RevealFileLocationDetail = {
      path: resolvedPath,
      line,
      column: 1,
    };
    const dispatchReveal = () => {
      window.dispatchEvent(new CustomEvent<RevealFileLocationDetail>(REVEAL_FILE_LOCATION_EVENT, { detail }));
    };
    dispatchReveal();
    window.requestAnimationFrame(dispatchReveal);
    window.setTimeout(dispatchReveal, 160);
    window.setTimeout(dispatchReveal, 360);
  };

  const toggleNode = (key: string) => {
    setExpandedNodes((current) => ({
      ...current,
      [key]: !current[key],
    }));
  };

  const renderDirectory = (node: QualityDirectoryNode, depth: number, isRoot: boolean = false): JSX.Element => {
    const directoryChildren = Array.from(node.directories.values()).sort((a, b) => a.name.localeCompare(b.name));
    const fileChildren = [...node.files].sort((a, b) => a.name.localeCompare(b.name));
    const isExpanded = isRoot ? true : Boolean(expandedNodes[node.key]);

    return (
      <li key={node.key}>
        {!isRoot && (
          <button
            className="tree-node quality-tree-node quality-tree-dir-row"
            style={{ paddingLeft: 6 + depth * 14 }}
            onClick={() => toggleNode(node.key)}
          >
            <ChevronRight size={12} className={`tree-chevron ${isExpanded ? 'open' : ''}`} />
            {isExpanded ? (
              <FolderOpen size={13} style={{ color: 'var(--accent-2)', flexShrink: 0 }} />
            ) : (
              <Folder size={13} style={{ color: 'var(--accent-2)', flexShrink: 0 }} />
            )}
            <span className="tree-name">{node.name}</span>
          </button>
        )}

        {isExpanded && (
          <ul className="quality-tree-list" role={isRoot ? 'tree' : 'group'}>
            {directoryChildren.map((child) => renderDirectory(child, depth + 1))}
            {fileChildren.map((file) => {
              const fileExpanded = Boolean(expandedNodes[file.key]);
              return (
                <li key={file.key} role="treeitem" aria-expanded={fileExpanded}>
                  <button
                    className="tree-node quality-tree-node quality-tree-file-row"
                    style={{ paddingLeft: 6 + (depth + 1) * 14 }}
                    onClick={() => toggleNode(file.key)}
                  >
                    <ChevronRight size={12} className={`tree-chevron ${fileExpanded ? 'open' : ''}`} />
                    <FileText size={13} style={{ color: 'var(--tx-3)', flexShrink: 0 }} />
                    <span className="tree-name">{file.name}</span>
                    <span className="quality-file-count">{file.issues.length}</span>
                  </button>

                  {fileExpanded && (
                    <ul className="quality-tree-issue-list" role="group">
                      {file.groups
                        .filter((group) => !shouldHideAsLowDuplicateNoise(group, showLowDuplicateNoise))
                        .map((group) => {
                        const groupExpanded = Boolean(expandedNodes[group.key]);
                        const firstIssue = group.issues[0];
                        return (
                          <li key={group.key}>
                            <button
                              className={`tree-node quality-tree-node quality-tree-group-node ${group.severity}`}
                              style={{ paddingLeft: 6 + (depth + 2) * 14 + 16 }}
                              onClick={() => {
                                if (firstIssue && group.issues.length === 1) {
                                  openIssueInEditor(firstIssue.file, firstIssue.line);
                                  return;
                                }
                                toggleNode(group.key);
                              }}
                              title={firstIssue ? `Open ${firstIssue.file}:${firstIssue.line}` : undefined}
                            >
                              <ChevronRight size={12} className={`tree-chevron ${groupExpanded ? 'open' : ''}`} />
                              <span className={`quality-severity-pill ${group.severity}`}>{group.severity.toUpperCase()}</span>
                              <span className="quality-issue-category">{group.category}</span>
                              <span className="quality-file-count">{group.issues.length}</span>
                              <span className="quality-issue-message">Task: {group.representativeMessage}</span>
                            </button>
                            {group.representativeSuggestion && (
                              <div className="quality-issue-suggestion">{group.representativeSuggestion}</div>
                            )}

                            {groupExpanded && (
                              <div className="quality-group-lines" style={{ paddingLeft: 6 + (depth + 3) * 14 + 16 }}>
                                {group.issues.slice(0, 24).map((issue) => (
                                  <button
                                    key={issue.id}
                                    className="quality-line-chip"
                                    onClick={(event) => {
                                      event.stopPropagation();
                                      openIssueInEditor(issue.file, issue.line);
                                    }}
                                    title={`Open ${issue.file}:${issue.line}`}
                                  >
                                    L{issue.line}
                                  </button>
                                ))}
                                {group.issues.length > 24 && (
                                  <span className="quality-more-lines">+{group.issues.length - 24} more</span>
                                )}
                              </div>
                            )}
                          </li>
                        );
                      })}
                    </ul>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </li>
    );
  };

  return (
    <div className="quality-panel-root">
      {qualityLoading && (
        <div className="quality-scan-topbar" aria-label="quality-scan-topbar">
          <div className="quality-scan-topbar-fill" style={{ width: `${progressPercent}%` }} />
        </div>
      )}

      <div className="quality-panel-header">
        <div className="quality-panel-title">Centralized quality index</div>
        <div className="quality-panel-subtitle">File-first view for duplicates, docs drift, dead code, and comment gaps.</div>
      </div>

      <div className="quality-panel-progress-line">
        <span>{progressLabel}</span>
        <span>{progressText}</span>
      </div>

      {!hasLoadedReport && (
        <div className="quality-scan-empty-state">
          <div className="quality-scan-description">
            Deterministic index for dead-code candidates, duplicate logic, large uncommented blocks, and documentation drift. Generated files are filtered first so build artifacts stay out of findings.
          </div>
          <div className="quality-scan-progress-label">{progressText}</div>
        </div>
      )}

      {qualityError && (
        <div className="preferences-message" style={{ borderColor: 'rgba(239, 68, 68, 0.4)' }}>
          {qualityError}
        </div>
      )}

      {likelyStaleReport && (
        <div className="preferences-message" style={{ borderColor: 'rgba(243, 201, 111, 0.45)' }}>
          This scan returned an unusually high issue volume. This usually means stale quality cache or an older scanner build is still running. Restart the backend and rescan.
        </div>
      )}

      {qualityReport && (
        <>
          <div className="quality-panel-summary">
            <div className="preferences-quality-stats">
              <span>Total: {qualityReport.issueCount}</span>
              <span>High: {qualityReport.highCount}</span>
              <span>Medium: {qualityReport.mediumCount}</span>
              <span>Low: {qualityReport.lowCount}</span>
            </div>
            <div className="quality-panel-meta">Explorer-style hierarchy. Top paths are expanded by default.</div>
          </div>

          <div className="quality-browser-section">
            <div className="quality-browser-section-header">Open Editors</div>
            {sortedOpenEditors.length === 0 ? (
              <div className="quality-browser-empty">No open editors</div>
            ) : (
              <ul className="quality-open-editors-list">
                {sortedOpenEditors.map((file) => {
                  const fileName = file.path.split('/').pop() ?? file.path;
                  const issueCount = issuesByFile.get(file.path) ?? 0;
                  return (
                    <li key={file.path}>
                      <button
                        className={`quality-open-editor-row ${file.path === activeFilePath ? 'active' : ''}`}
                        onClick={() => setActiveFile(file.path)}
                        title={file.path}
                      >
                        <span className="quality-open-editor-name">{fileName}</span>
                        <span className="quality-open-editor-meta">
                          {file.dirty ? <span className="quality-open-editor-dirty">unsaved</span> : null}
                          <span className="quality-open-editor-issues">{issueCount}</span>
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          {qualityReport.issueCount === 0 ? (
            <div className="quality-empty">No quality findings for this project scan.</div>
          ) : (
            <div className="quality-browser-section">
              <div className="quality-browser-section-header">Quality Explorer</div>
              <div className="quality-toolbar-row">
                <button
                  className={`quality-toolbar-toggle ${showLowDuplicateNoise ? 'active' : ''}`}
                  onClick={() => setShowLowDuplicateNoise((current) => !current)}
                  title="Toggle low-severity duplicate-content noise"
                >
                  {showLowDuplicateNoise ? 'Hide' : 'Show'} low duplicate noise
                </button>
                <span className="quality-toolbar-meta">Task groups: {visibleGroupCount}</span>
              </div>
              {!showLowDuplicateNoise && hiddenLowDuplicateIssueCount > 0 && (
                <div className="quality-noise-banner">
                  Hidden {hiddenLowDuplicateIssueCount} low-severity duplicate findings to keep this actionable. Counts above remain unchanged.
                </div>
              )}
              <ul className="quality-tree-list" role="tree">
                {renderDirectory(qualityIssueTree, -1, true)}
              </ul>
            </div>
          )}
        </>
      )}
    </div>
  );
}
