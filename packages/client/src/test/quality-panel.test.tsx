import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import QualityPanel from '../components/Quality/QualityPanel.js';
import { useStore } from '../store/index.js';

function resetQualityState() {
  useStore.setState({
    qualityReport: null,
    qualityProgress: null,
    qualityLoading: false,
    qualityCompleted: false,
    qualityError: null,
  });
}

describe('QualityPanel', () => {
  beforeEach(() => {
    resetQualityState();
  });

  it('QualityPanel_ScanNotCompleted_ShowsCenteredProgressAndDescription', () => {
    useStore.setState({
      qualityLoading: true,
      qualityCompleted: false,
      qualityProgress: {
        projectPath: '/tmp/p',
        phase: 'scan',
        current: 3,
        total: 12,
        percent: 25,
        currentFile: 'packages/server-go/quality/report.go',
        currentFunction: 'analyzeFile',
        section: 'scan',
      },
    });

    render(<QualityPanel />);

    expect(screen.getByLabelText('quality-scan-largebar')).toBeTruthy();
    expect(screen.getByText(/Scanning project - packages\/server-go\/quality\/report.go\|analyzeFile\(\/\/scan\)/)).toBeTruthy();
  });

  it('QualityPanel_ScanInProgressAfterCompletion_ShowsTinyTopBar', () => {
    useStore.setState({
      qualityLoading: true,
      qualityCompleted: true,
      qualityProgress: {
        projectPath: '/tmp/p',
        phase: 'report',
        current: 40,
        total: 100,
        percent: 40,
      },
    });

    render(<QualityPanel />);

    expect(screen.getByLabelText('quality-scan-topbar')).toBeTruthy();
  });

  it('QualityPanel_ScanInProgressWithLoadedReport_UsesTopBarOnly', () => {
    useStore.setState({
      qualityLoading: true,
      qualityCompleted: false,
      qualityReport: {
        projectPath: '/tmp/p',
        generatedAt: new Date().toISOString(),
        issueCount: 1,
        highCount: 0,
        mediumCount: 1,
        lowCount: 0,
        issues: [
          {
            id: 'q1',
            severity: 'medium',
            category: 'docs',
            message: 'Missing docs',
            file: 'a.ts',
            line: 4,
          },
        ],
      },
      qualityProgress: {
        projectPath: '/tmp/p',
        phase: 'scan',
        current: 2,
        total: 10,
        percent: 20,
      },
    });

    render(<QualityPanel />);

    expect(screen.getByLabelText('quality-scan-topbar')).toBeTruthy();
    expect(screen.queryByLabelText('quality-scan-largebar')).toBeNull();
  });

  it('QualityPanel_ReportSuccess_RendersExplorerGroupsAndIssueStats', () => {
    useStore.setState({
      qualityCompleted: true,
      qualityReport: {
        projectPath: '/tmp/p',
        generatedAt: new Date().toISOString(),
        issueCount: 2,
        highCount: 1,
        mediumCount: 1,
        lowCount: 0,
        issues: [
          {
            id: 'q1',
            severity: 'high',
            category: 'dead-code',
            message: 'Unused function',
            file: 'a.ts',
            line: 3,
            suggestion: 'Delete it',
          },
          {
            id: 'q2',
            severity: 'medium',
            category: 'documentation-gap',
            message: 'Missing docs',
            file: 'b.ts',
            line: 8,
          },
        ],
      },
    });

    render(<QualityPanel />);

    expect(screen.getByText('Total: 2')).toBeTruthy();
    expect(screen.getByText('High: 1')).toBeTruthy();
    expect(screen.getByText('Medium: 1')).toBeTruthy();
    expect(screen.getByText('Low: 0')).toBeTruthy();
    expect(screen.getByText('Unused function')).toBeTruthy();
    expect(screen.getByText('Delete it')).toBeTruthy();
    expect(screen.getByText('Missing docs')).toBeTruthy();
    expect(screen.getByText('a.ts')).toBeTruthy();
    expect(screen.getByText('b.ts')).toBeTruthy();
  });

  it('QualityPanel_ReportSuccess_SortsIssuesBySeverityThenLineWithinFile', () => {
    useStore.setState({
      qualityCompleted: true,
      qualityReport: {
        projectPath: '/tmp/p',
        generatedAt: new Date().toISOString(),
        issueCount: 4,
        highCount: 1,
        mediumCount: 1,
        lowCount: 2,
        issues: [
          {
            id: 'low-late',
            severity: 'low',
            category: 'style',
            message: 'Low line 40',
            file: 'same.ts',
            line: 40,
          },
          {
            id: 'medium',
            severity: 'medium',
            category: 'docs',
            message: 'Medium line 30',
            file: 'same.ts',
            line: 30,
          },
          {
            id: 'high',
            severity: 'high',
            category: 'bug',
            message: 'High line 50',
            file: 'same.ts',
            line: 50,
          },
          {
            id: 'low-early',
            severity: 'low',
            category: 'style',
            message: 'Low line 10',
            file: 'same.ts',
            line: 10,
          },
        ],
      },
    });

    render(<QualityPanel />);

    const messages = screen.getAllByText(/line /).map((node) => node.textContent);
    expect(messages).toEqual([
      'High line 50',
      'Medium line 30',
      'Low line 10',
      'Low line 40',
    ]);
  });

  it('QualityPanel_CompletedWithNoFindings_ShowsEmptyStateMessage', () => {
    useStore.setState({
      qualityCompleted: true,
      qualityReport: {
        projectPath: '/tmp/p',
        generatedAt: new Date().toISOString(),
        issueCount: 0,
        highCount: 0,
        mediumCount: 0,
        lowCount: 0,
        issues: [],
      },
    });

    render(<QualityPanel />);

    expect(screen.getByText('No quality findings for this project scan.')).toBeTruthy();
  });

  it('QualityPanel_NoProgressYet_ShowsWaitingCopy', () => {
    render(<QualityPanel />);

    expect(screen.getByText('Waiting for automatic project scan...')).toBeTruthy();
  });

  it('QualityPanel_LoadingWithoutProgress_ShowsStartFallbackCopy', () => {
    useStore.setState({
      qualityLoading: true,
      qualityCompleted: false,
      qualityProgress: null,
    });

    render(<QualityPanel />);

    expect(screen.getByText('Scanning project - project|scan(//start)')).toBeTruthy();
  });

  it('QualityPanel_Error_ShowsErrorMessage', () => {
    useStore.setState({ qualityError: 'scan failed' });
    render(<QualityPanel />);
    expect(screen.getByText('scan failed')).toBeTruthy();
  });
});
