package main

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

//go:embed icons/alert-triangle.svg
var iconAlertSVG []byte

//go:embed icons/chevron-down.svg
var iconChevronDownSVG []byte

//go:embed icons/chevron-right.svg
var iconChevronRightSVG []byte

//go:embed icons/columns-2.svg
var iconColumns2SVG []byte

//go:embed icons/external-link.svg
var iconExternalLinkSVG []byte

//go:embed icons/file-code-2.svg
var iconFileCodeSVG []byte

//go:embed icons/file-text.svg
var iconFileTextSVG []byte

//go:embed icons/image.svg
var iconImageSVG []byte

//go:embed icons/keyboard.svg
var iconKeyboardSVG []byte

//go:embed icons/folder.svg
var iconFolderSVG []byte

//go:embed icons/folder-git-2.svg
var iconFolderGitSVG []byte

//go:embed icons/folder-open.svg
var iconFolderOpenSVG []byte

//go:embed icons/folder-plus.svg
var iconFolderPlusSVG []byte

//go:embed icons/git-branch.svg
var iconGitBranchSVG []byte

//go:embed icons/git-compare-arrows.svg
var iconGitCompareSVG []byte

//go:embed icons/globe-2.svg
var iconGlobeSVG []byte

//go:embed icons/info.svg
var iconInfoSVG []byte

//go:embed icons/minus.svg
var iconMinusSVG []byte

//go:embed icons/open-in-new.svg
var iconOpenInNewSVG []byte

//go:embed icons/panel-left.svg
var iconPanelLeftSVG []byte

//go:embed icons/panel-left-close.svg
var iconPanelLeftCloseSVG []byte

//go:embed icons/panel-right.svg
var iconPanelRightSVG []byte

//go:embed icons/plus.svg
var iconPlusSVG []byte

//go:embed icons/radio.svg
var iconRadioSVG []byte

//go:embed icons/refresh-cw.svg
var iconRefreshSVG []byte

//go:embed icons/rotate-cw.svg
var iconRotateSVG []byte

//go:embed icons/rows-2.svg
var iconRows2SVG []byte

//go:embed icons/search.svg
var iconSearchSVG []byte

//go:embed icons/terminal.svg
var iconTerminalSVG []byte

//go:embed icons/trash-2.svg
var iconTrashSVG []byte

//go:embed icons/undo-2.svg
var iconUndoSVG []byte

//go:embed icons/x.svg
var iconXSVG []byte

//go:embed icons/save.svg
var iconSaveSVG []byte

//go:embed icons/git-fork.svg
var iconGitGraphSVG []byte

//go:embed icons/archive.svg
var iconArchiveSVG []byte

//go:embed icons/arrow-down.svg
var iconArrowDownSVG []byte

//go:embed icons/arrow-up.svg
var iconArrowUpSVG []byte

//go:embed icons/download.svg
var iconDownloadSVG []byte

//go:embed icons/square-terminal.svg
var iconSquareTerminalSVG []byte

//go:embed icons/check.svg
var iconCheckSVG []byte

//go:embed icons/settings.svg
var iconSettingsSVG []byte

//go:embed icons/palette.svg
var iconPaletteSVG []byte

//go:embed icons/code.svg
var iconCodeSVG []byte

//go:embed icons/bot.svg
var iconBotSVG []byte

//go:embed icons/smartphone.svg
var iconSmartphoneSVG []byte

//go:embed icons/chevrons-up-down.svg
var iconChevronsUpDownSVG []byte

//go:embed icons/file-cog.svg
var iconFileCogSVG []byte

var (
	iconAlert          = ui.MustParseSVG(iconAlertSVG)
	iconChevronDown    = ui.MustParseSVG(iconChevronDownSVG)
	iconChevronRight   = ui.MustParseSVG(iconChevronRightSVG)
	iconColumns2       = ui.MustParseSVG(iconColumns2SVG)
	iconExternalLink   = ui.MustParseSVG(iconExternalLinkSVG)
	iconFileCode       = ui.MustParseSVG(iconFileCodeSVG)
	iconFileText       = ui.MustParseSVG(iconFileTextSVG)
	iconImage          = ui.MustParseSVG(iconImageSVG)
	iconKeyboard       = ui.MustParseSVG(iconKeyboardSVG)
	iconFolder         = ui.MustParseSVG(iconFolderSVG)
	iconFolderGit      = ui.MustParseSVG(iconFolderGitSVG)
	iconFolderOpen     = ui.MustParseSVG(iconFolderOpenSVG)
	iconFolderPlus     = ui.MustParseSVG(iconFolderPlusSVG)
	iconGitBranch      = ui.MustParseSVG(iconGitBranchSVG)
	iconGitCompare     = ui.MustParseSVG(iconGitCompareSVG)
	iconGlobe          = ui.MustParseSVG(iconGlobeSVG)
	iconInfo           = ui.MustParseSVG(iconInfoSVG)
	iconMinus          = ui.MustParseSVG(iconMinusSVG)
	iconOpenInNew      = ui.MustParseSVG(iconOpenInNewSVG)
	iconPanelLeft      = ui.MustParseSVG(iconPanelLeftSVG)
	iconPanelLeftClose = ui.MustParseSVG(iconPanelLeftCloseSVG)
	iconPanelRight     = ui.MustParseSVG(iconPanelRightSVG)
	iconPlus           = ui.MustParseSVG(iconPlusSVG)
	iconRadio          = ui.MustParseSVG(iconRadioSVG)
	iconRefresh        = ui.MustParseSVG(iconRefreshSVG)
	iconRotate         = ui.MustParseSVG(iconRotateSVG)
	iconRows2          = ui.MustParseSVG(iconRows2SVG)
	iconSearch         = ui.MustParseSVG(iconSearchSVG)
	iconTerminal       = ui.MustParseSVG(iconTerminalSVG)
	iconTrash          = ui.MustParseSVG(iconTrashSVG)
	iconUndo           = ui.MustParseSVG(iconUndoSVG)
	iconX              = ui.MustParseSVG(iconXSVG)
	iconSave           = ui.MustParseSVG(iconSaveSVG)
	iconGitGraph       = ui.MustParseSVG(iconGitGraphSVG)
	iconArchive        = ui.MustParseSVG(iconArchiveSVG)
	iconArrowDown      = ui.MustParseSVG(iconArrowDownSVG)
	iconArrowUp        = ui.MustParseSVG(iconArrowUpSVG)
	iconDownload       = ui.MustParseSVG(iconDownloadSVG)
	iconSquareTerminal = ui.MustParseSVG(iconSquareTerminalSVG)
	iconCheck          = ui.MustParseSVG(iconCheckSVG)
	iconSettings       = ui.MustParseSVG(iconSettingsSVG)
	iconPalette        = ui.MustParseSVG(iconPaletteSVG)
	iconCode           = ui.MustParseSVG(iconCodeSVG)
	iconBot            = ui.MustParseSVG(iconBotSVG)
	iconSmartphone     = ui.MustParseSVG(iconSmartphoneSVG)
	iconChevronsUpDown = ui.MustParseSVG(iconChevronsUpDownSVG)
	iconFileCog        = ui.MustParseSVG(iconFileCogSVG)
)
