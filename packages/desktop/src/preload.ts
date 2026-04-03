import { contextBridge, ipcRenderer } from 'electron';

contextBridge.exposeInMainWorld('electronAPI', {
  getProjectPath: (): Promise<string> => ipcRenderer.invoke('get-project-path'),
  getGithubToken: (): Promise<string | null> => ipcRenderer.invoke('get-github-token'),
  setGithubToken: (token: string): Promise<boolean> => ipcRenderer.invoke('set-github-token', token),
  openExternal: (url: string): Promise<void> => ipcRenderer.invoke('open-external', url),
  platform: process.platform,
  isElectron: true,
});
