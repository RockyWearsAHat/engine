import { useEffect, useMemo, useState } from 'react';
import { useStore } from '../../store/index.js';
import { wsClient } from '../../ws/client.js';
import { REVEAL_FILE_LOCATION_EVENT, type RevealFileLocationDetail } from '../../editorEvents.js';

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
    const groups = new Map<string, NonNullable<typeof qualityReport>['issues']>();
    if (!qualityReport) {
      return [];
    }
    for (const issue of qualityReport.issues) {
      const group = groups.get(issue.file) ?? [];
      group.push(issue);
      groups.set(issue.file, group);
    }
    return Array.from(groups.entries())
      .map(([file, issues]) => [
        file,
        [...issues].sort((a, b) => {
          const severityRank = (severity: string) => {
            switch (severity) {
              case 'high':
                return 0;
              case 'medium':
                return 1;
              default:
                return 2;
            }
          };
          if (severityRank(a.severity) !== severityRank(b.severity)) {
            return severityRank(a.severity) - severityRank(b.severity);
          }
          return a.line - b.line;
        }),
      ] as const)
      .sort((a, b) => a[0].localeCompare(b[0]));
  }, [qualityReport]);

  const progressPercent = Math.max(0, Math.min(100, Math.round(qualityProgress?.percent ?? 0)));
  const hasLoadedReport = qualityReport !== null;
  const hasStartedScan = qualityLoading || qualityProgress !== null;
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
  const [expandedFiles, setExpandedFiles] = useState<Record<string, boolean>>({});

  useEffect(() => {
    const defaults: Record<string, boolean> = {};
    groupedIssues.slice(0, 3).forEach(([file]) => {
      defaults[file] = true;
    });
    setExpandedFiles(defaults);
  }, [groupedIssues]);

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
    wsClient.send({ type: 'file.read', path: resolvedPath });
    const detail: RevealFileLocationDetail = {
      path: resolvedPath,
      line,
      column: 1,
    };
    window.dispatchEvent(new CustomEvent<RevealFileLocationDetail>(REVEAL_FILE_LOCATION_EVENT, { detail }));
    window.setTimeout(() => {
      window.dispatchEvent(new CustomEvent<RevealFileLocationDetail>(REVEAL_FILE_LOCATION_EVENT, { detail }));
    }, 120);
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

      {qualityReport && (
        <>
          <div className="quality-panel-summary">
            <div className="preferences-quality-stats">
              <span>Total: {qualityReport.issueCount}</span>
              <span>High: {qualityReport.highCount}</span>
              <span>Medium: {qualityReport.mediumCount}</span>
              <span>Low: {qualityReport.lowCount}</span>
            </div>
            <div className="quality-panel-meta">Grouped by file. Top 3 files expanded by default.</div>
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
              <div className="quality-browser-section-header">Findings by File</div>
              <ul className="quality-tree-list" role="tree">
                {groupedIssues.map(([file, issues]) => {
                  const isExpanded = Boolean(expandedFiles[file]);
                  return (
                    <li key={file} className="quality-tree-file" role="treeitem" aria-expanded={isExpanded}>
                      <button
                        className="quality-tree-file-row"
                        onClick={() => {
                          setExpandedFiles((current) => ({
                            ...current,
                            [file]: !current[file],
                          }));
                        }}
                      >
                        <span className="quality-tree-chevron" aria-hidden="true">{isExpanded ? '▾' : '▸'}</span>
                        <span className="quality-file-name">{file}</span>
                        <span className="quality-file-count">{issues.length}</span>
                      </button>
                      {isExpanded && (
                        <ul className="quality-tree-issue-list" role="group">
                          {issues.map((issue) => (
                            <li key={issue.id}>
                              <button
                                className={`quality-tree-issue-row ${issue.severity}`}
                                onClick={() => openIssueInEditor(issue.file, issue.line)}
                                title={`Open ${issue.file}:${issue.line}`}
                              >
                                <div className="preferences-quality-meta">
                                  <span className={`quality-severity-pill ${issue.severity}`}>{issue.severity.toUpperCase()}</span>
                                  <span>{issue.category}</span>
                                  <span>L{issue.line}</span>
                                </div>
                                <div className="quality-issue-message">{issue.message}</div>
                                {issue.suggestion && <small className="quality-issue-suggestion">{issue.suggestion}</small>}
                              </button>
                            </li>
                          ))}
                        </ul>
                      )}
                    </li>
                  );
                })}
              </ul>
            </div>
          )}
        </>
      )}
    </div>
  );
}
