package internal

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ShowModernFolderPicker opens the modern Windows Vista/7/10/11 Explorer folder dialog
// (IFileOpenDialog with FOS_PICKFOLDERS, matching "Save As" / "Open File" styling)
// instead of the legacy tree-view FolderBrowserDialog.
func ShowModernFolderPicker(title string, initialDir string) (string, error) {
	if runtime.GOOS != "windows" {
		return "", nil
	}

	// Escape single quotes for PowerShell literal string parameters
	escapedTitle := strings.ReplaceAll(title, "'", "''")
	escapedInitial := strings.ReplaceAll(initialDir, "'", "''")

	script := fmt.Sprintf(`& {
    param($Title, $InitialDir)
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    try {
        Add-Type -TypeDefinition @"
        using System;
        using System.Runtime.InteropServices;

        public class ModernFolderPicker {
            [DllImport("shell32.dll")]
            private static extern int SHCreateItemFromParsingName([MarshalAs(UnmanagedType.LPWStr)] string pszPath, IntPtr pbc, [In, MarshalAs(UnmanagedType.LPStruct)] Guid riid, out IntPtr ppv);

            [ComImport, Guid("D57C7288-D4AD-4768-BE02-9D969532D960"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
            private interface IFileOpenDialog {
                [PreserveSig] int Show(IntPtr parent);
                void SetFileTypes(); void SetFileTypeIndex(); void GetFileTypeIndex(); void Advise(); void Unadvise();
                void SetOptions(uint fos); void GetOptions(out uint fos); void SetDefaultFolder(IntPtr psi); void SetFolder(IntPtr psi);
                void GetFolder(out IntPtr ppsi); void GetCurrentSelection(out IntPtr ppsi);
                void SetFileName([MarshalAs(UnmanagedType.LPWStr)] string pszName);
                void GetFileName([MarshalAs(UnmanagedType.LPWStr)] out string pszName);
                void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string pszTitle);
                void SetOkButtonLabel([MarshalAs(UnmanagedType.LPWStr)] string pszText);
                void SetFileNameLabel([MarshalAs(UnmanagedType.LPWStr)] string pszLabel);
                void GetResult(out IntPtr ppsi);
                void AddPlace(); void SetDefaultExtension(); void Close(); void SetClientGuid(); void ClearClientData();
                void SetFilter(); void GetResults(); void GetSelectedItems();
            }

            [ComImport, Guid("43826D1E-E718-42EE-BC55-A1E261C37BFE"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
            private interface IShellItem {
                void BindToHandler(); void GetParent();
                void GetDisplayName(uint sigdnName, [MarshalAs(UnmanagedType.LPWStr)] out string ppszName);
                void GetAttributes(); void Compare();
            }

            [ComImport, Guid("DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7"), ClassInterface(ClassInterfaceType.None)]
            private class FileOpenDialogRCW {}

            public static string ShowDialog(string title, string initialFolder) {
                var dialog = (IFileOpenDialog)new FileOpenDialogRCW();
                // FOS_PICKFOLDERS (0x20) | FOS_FORCEFILESYSTEM (0x40) | FOS_PATHMUSTEXIST (0x8) | FOS_FILEMUSTEXIST (0x4)
                uint fos = 0x00000020 | 0x00000040 | 0x00000008 | 0x00000004;
                dialog.SetOptions(fos);
                if (!string.IsNullOrEmpty(title)) {
                    dialog.SetTitle(title);
                    dialog.SetOkButtonLabel("Select Folder");
                }
                if (!string.IsNullOrEmpty(initialFolder) && System.IO.Directory.Exists(initialFolder)) {
                    Guid iid = new Guid("43826D1E-E718-42EE-BC55-A1E261C37BFE");
                    IntPtr psiPtr;
                    if (SHCreateItemFromParsingName(initialFolder, IntPtr.Zero, iid, out psiPtr) == 0) {
                        dialog.SetFolder(psiPtr);
                        Marshal.Release(psiPtr);
                    }
                }
                int hr = dialog.Show(IntPtr.Zero);
                if (hr == 0) {
                    IntPtr itemPtr;
                    dialog.GetResult(out itemPtr);
                    var item = (IShellItem)Marshal.GetObjectForIUnknown(itemPtr);
                    string path;
                    item.GetDisplayName(0x80058000, out path); // SIGDN_FILESYSPATH
                    Marshal.Release(itemPtr);
                    return path;
                }
                return null;
            }
        }
"@
        $res = [ModernFolderPicker]::ShowDialog($Title, $InitialDir)
        if ($res) {
            Write-Output $res
            exit
        }
    } catch {
        Add-Type -AssemblyName System.Windows.Forms
        $fb = New-Object System.Windows.Forms.FolderBrowserDialog
        if ($Title) { $fb.Description = $Title }
        if ($fb.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
            Write-Output $fb.SelectedPath
            exit
        }
    }
} '%s' '%s'`, escapedTitle, escapedInitial)

	cmd := exec.Command("powershell", "-NoProfile", "-Sta", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}
