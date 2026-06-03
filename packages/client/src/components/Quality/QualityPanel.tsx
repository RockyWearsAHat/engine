import { useMemo } from 'react';
import { useStore } from '../../store/index.js';

export default function QualityPanel() {
  const {
    qualityReport,
    qualityLoading,
    qualityCompleted,
    qualityError,
    qualityProgress,
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
  const hasStartedScan = qualityLoading || qualityProgress !== null;
  const progressText = qualityProgress
    ? `Scanning project - ${qualityProgress.currentFile || 'project'}|${qualityProgress.currentFunction || 'scan'}(//${qualityProgress.section || qualityProgress.phase})`
    : hasStartedScan
      ? 'Scanning project - project|scan(//start)'
      : 'Waiting for automatic project scan...';

  return (
    <div className="quality-panel-root">
      {qualityLoading && qualityCompleted && (
        <div className="quality-scan-topbar" aria-label="quality-scan-topbar">
          <div className="quality-scan-topbar-fill" style={{ width: `${progressPercent}%` }} />
        </div>
      )}

      {!qualityCompleted && (
        <div className="quality-scan-empty-state">
          <div className="quality-scan-description">
            Deterministic codebase index for dead-code candidates, duplicate logic, large uncommented blocks, documentation drift, and CS 2420/3500 contention across the project. Generated-file paths are mapped first so build artifacts stay out of quality findings.
          </div>
          <div className="quality-scan-largebar" aria-label="quality-scan-largebar">
            <div className="quality-scan-largebar-fill" style={{ width: `${progressPercent}%` }} />
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
            <div className="quality-panel-meta">Grouped by file. Top 3 groups expanded by default.</div>
          </div>

          {qualityReport.issueCount === 0 ? (
            <div className="quality-empty">No quality findings for this project scan.</div>
          ) : (
            <div className="quality-explorer-view">
              {groupedIssues.map(([file, issues], index) => (
                <details key={file} className="quality-explorer-file-group" open={index < 3}>
                  <summary className="quality-explorer-file-header">
                    <span className="quality-file-name">{file}</span>
                    <span className="quality-file-count">{issues.length}</span>
                  </summary>
                  <ul className="preferences-quality-list">
                    {issues.map((issue) => (
                      <li key={issue.id} className={`preferences-quality-item ${issue.severity}`}>
                        <div className="preferences-quality-meta">
                          <span className={`quality-severity-pill ${issue.severity}`}>{issue.severity.toUpperCase()}</span>
                          <span>{issue.category}</span>
                          <span>L{issue.line}</span>
                        </div>
                        <div className="quality-issue-message">{issue.message}</div>
                        {issue.suggestion && <small className="quality-issue-suggestion">{issue.suggestion}</small>}
                      </li>
                    ))}
                  </ul>
                </details>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}
