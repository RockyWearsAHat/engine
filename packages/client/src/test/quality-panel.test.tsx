import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import QualityPanel from '../components/Quality/QualityPanel.js';
import { useStore } from '../store/index.js';

const { wsSendMock } = vi.hoisted(() => ({
  wsSendMock: vi.fn(),
}));

vi.mock('../ws/client.js', () => ({
  wsClient: {
    send: wsSendMock,
  },
}));

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
    wsSendMock.mockReset();
    resetQualityState();
  });

  it('QualityPanel_ScanNotCompleted_ShowsTopProgressBarAndCentralizedDescription', () => {
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

    expect(screen.getByLabelText('quality-scan-topbar')).toBeTruthy();
    expect(screen.getByText('Centralized quality index')).toBeTruthy();
    expect(screen.getAllByText(/Scanning project - packages\/server-go\/quality\/report.go\|analyzeFile\(\/\/scan\)/)).toHaveLength(2);
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
  });

  it('QualityPanel_ReportSuccess_RendersExplorerGroupsAndIssueStats', () => {
    useStore.setState({
      openFiles: [
        {
          path: 'a.ts',
          content: 'export const a = 1',
          language: 'typescript',
          size: 20,
          largeFile: false,
          dirty: true,
        },
      ],
      activeFilePath: 'a.ts',
    });
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
    expect(screen.getByText('Task: Unused function')).toBeTruthy();
    expect(screen.getByText('Delete it')).toBeTruthy();
    expect(screen.getByText('Task: Missing docs')).toBeTruthy();
    expect(screen.getAllByText('a.ts').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('b.ts')).toBeTruthy();
    expect(screen.getByText('Open Editors')).toBeTruthy();
    expect(screen.getByText('unsaved')).toBeTruthy();
  });

  it('QualityPanel_ReportSuccess_RendersHierarchicalDirectoriesLikeExplorer', () => {
    useStore.setState({
      qualityCompleted: true,
      qualityReport: {
        projectPath: '/tmp/project',
        generatedAt: new Date().toISOString(),
        issueCount: 1,
        highCount: 0,
        mediumCount: 1,
        lowCount: 0,
        issues: [
          {
            id: 'nested-1',
            severity: 'medium',
            category: 'documentation-gap',
            message: 'Missing contract docs',
            file: 'packages/client/src/App.tsx',
            line: 12,
          },
        ],
      },
    });

    render(<QualityPanel />);

    expect(screen.getByText('packages')).toBeTruthy();
    expect(screen.getByText('client')).toBeTruthy();
    expect(screen.getByText('src')).toBeTruthy();
    expect(screen.getByText('App.tsx')).toBeTruthy();
    expect(screen.getByText('Task: Missing contract docs')).toBeTruthy();
  });

  it('QualityPanel_ReportSuccess_GroupsSameSeverityAndCategoryIntoSingleTaskRow', () => {
    useStore.setState({
      qualityCompleted: true,
      qualityReport: {
        projectPath: '/tmp/project',
        generatedAt: new Date().toISOString(),
        issueCount: 3,
        highCount: 0,
        mediumCount: 3,
        lowCount: 0,
        issues: [
          {
            id: 'group-1',
            severity: 'medium',
            category: 'large-block-without-comment',
            message: 'Split into smaller helpers and annotate non-obvious intent.',
            file: 'packages/client/src/App.tsx',
            line: 115,
          },
          {
            id: 'group-2',
            severity: 'medium',
            category: 'large-block-without-comment',
            message: 'Split into smaller helpers and annotate non-obvious intent.',
            file: 'packages/client/src/App.tsx',
            line: 203,
          },
          {
            id: 'group-3',
            severity: 'medium',
            category: 'large-block-without-comment',
            message: 'Split into smaller helpers and annotate non-obvious intent.',
            file: 'packages/client/src/App.tsx',
            line: 411,
          },
        ],
      },
    });

    render(<QualityPanel />);

    expect(screen.getByText('Total: 3')).toBeTruthy();
    expect(screen.getAllByText('large-block-without-comment').length).toBe(1);
    expect(screen.getByText('Task: Split into smaller helpers and annotate non-obvious intent.')).toBeTruthy();
    expect(screen.getAllByText('3').length).toBeGreaterThan(0);
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

    const messages = screen.getAllByText(/Task: .*line /).map((node) => node.textContent);
    expect(messages).toEqual([
      'Task: High line 50',
      'Task: Medium line 30',
      'Task: Low line 10',
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

    expect(screen.getAllByText('Waiting for automatic project scan...').length).toBeGreaterThanOrEqual(1);
  });

  it('QualityPanel_LoadingWithoutProgress_ShowsStartFallbackCopy', () => {
    useStore.setState({
      qualityLoading: true,
      qualityCompleted: false,
      qualityProgress: null,
    });

    render(<QualityPanel />);

    expect(screen.getAllByText('Scanning project - project|scan(//start)').length).toBeGreaterThanOrEqual(1);
  });

  it('QualityPanel_Error_ShowsErrorMessage', () => {
    useStore.setState({ qualityError: 'scan failed' });
    render(<QualityPanel />);
    expect(screen.getByText('scan failed')).toBeTruthy();
  });

  it('QualityPanel_ClickingIssue_OpensFileInEditorAtIssueLine', () => {
    useStore.setState({
      qualityCompleted: true,
      qualityReport: {
        projectPath: '/tmp/project',
        generatedAt: new Date().toISOString(),
        issueCount: 1,
        highCount: 1,
        mediumCount: 0,
        lowCount: 0,
        issues: [
          {
            id: 'q-open',
            severity: 'high',
            category: 'duplicate-content',
            message: 'Duplicate code detected',
            file: 'packages/client/src/App.tsx',
            line: 42,
          },
        ],
      },
    });

    render(<QualityPanel />);

    const issueButton = screen.getByTitle('Open packages/client/src/App.tsx:42');
    fireEvent.click(issueButton);

    expect(wsSendMock).toHaveBeenCalledWith({
      type: 'file.read',
      path: '/tmp/project/packages/client/src/App.tsx',
    });
  });
});
