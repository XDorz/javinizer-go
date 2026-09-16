package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/javinizer/javinizer-go/internal/database"
	"github.com/javinizer/javinizer-go/internal/fsutil"
	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/javinizer/javinizer-go/internal/operationmode"
	"github.com/javinizer/javinizer-go/internal/organizer"
	"github.com/spf13/afero"
)

type artifactPlanExecutor interface {
	PlanOrganize(context.Context, organizer.OrganizeCmd) (*organizer.OrganizePlan, error)
	PlanSourceExists(*organizer.OrganizePlan) bool
	ExecuteOrganizePlan(*organizer.OrganizePlan, bool, organizer.LinkMode) (*organizer.OrganizeResult, error)
}

type artifactSibling struct {
	sourcePath string
	stagedPath string
}

type artifactStage struct {
	fs            afero.Fs
	fencer        database.ApplyArtifactPublicationFencer
	root          string
	finalRoot     string
	sourcePath    string
	stagedSource  string
	siblings      []artifactSibling
	inPlace       bool
	original      ApplyCmd
	rejected      bool
	duplicatePlan *organizer.OrganizePlan
}

var errArtifactDirtyAdmission = errors.New("artifact publication preparation failed")

func artifactFencer(cmd ApplyCmd) database.ApplyArtifactPublicationFencer {
	fencer, _ := cmd.PublicationFence.(database.ApplyArtifactPublicationFencer)
	return fencer
}

func markArtifactDirty(cmd ApplyCmd) {
	fencer := artifactFencer(cmd)
	if fencer == nil || cmd.Movie == nil || strings.TrimSpace(cmd.Movie.ContentID) == "" {
		return
	}
	_ = fencer.WithApplyArtifactPublicationFence(context.Background(), cmd.Movie.ContentID, cmd.Movie.RenderGeneration, func(*models.Movie) error {
		return errArtifactDirtyAdmission
	})
}

func (o *applyOrchImpl) prepareArtifact(ctx context.Context, cmd ApplyCmd) (*artifactStage, ApplyCmd, error) {
	if cmd.DryRun {
		return nil, cmd, nil
	}
	needsArtifacts := !cmd.Organize.Skip || cmd.Download || cmd.GenerateNFO
	if needsArtifacts && cmd.PublicationFence != nil && artifactFencer(cmd) == nil {
		return nil, cmd, fmt.Errorf("artifact staging blocked: publication fence lacks artifact capability")
	}
	if artifactFencer(cmd) == nil {
		return nil, cmd, nil
	}
	if cmd.Movie == nil || strings.TrimSpace(cmd.Movie.ContentID) == "" {
		return nil, cmd, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, cmd, err
	}
	mode := cmd.OperationMode
	if mode == "" {
		mode = operationmode.OperationModeOrganize
	}
	inPlace := mode == operationmode.OperationModeInPlace || mode == operationmode.OperationModeInPlaceNoRenameFolder
	if cmd.Organize.Skip && !cmd.Download && !cmd.GenerateNFO {
		return nil, cmd, nil
	}
	sourcePath := strings.TrimSpace(cmd.Match.Path)
	finalRoot := strings.TrimSpace(cmd.DestPath)
	if finalRoot == "" {
		if !inPlace && mode != operationmode.OperationModeMetadataArtwork {
			return nil, cmd, fmt.Errorf("artifact staging requires a destination path")
		}
		if sourcePath == "" {
			return nil, cmd, fmt.Errorf("artifact staging requires a source path")
		}
		finalRoot = filepath.Dir(sourcePath)
	} else {
		finalRoot = filepath.Clean(finalRoot)
	}
	var duplicatePlan *organizer.OrganizePlan
	if tracker := cmd.Organize.DuplicateTracker; tracker != nil && !cmd.Organize.Skip {
		executor, ok := o.organizer.(artifactPlanExecutor)
		if !ok {
			return nil, cmd, fmt.Errorf("artifact staging blocked: organizer has no planned execution seam")
		}
		plan, planErr := executor.PlanOrganize(ctx, organizer.OrganizeCmd{Match: cmd.Match, Movie: cmd.Movie, DestDir: finalRoot, ForceUpdate: cmd.Organize.ForceUpdate, MoveFiles: cmd.Organize.MoveFiles, LinkMode: cmd.Organize.LinkMode, OperationMode: cmd.OperationMode, ForceRenameFile: cmd.Organize.ForceRenameFile})
		if planErr != nil {
			return nil, cmd, fmt.Errorf("plan artifact duplicate claim: %w", planErr)
		}
		_, duplicate := tracker.ObserveClaim(ctx, plan.SourcePath, plan.TargetPath, plan.WillMove)
		if duplicate {
			if !cmd.Organize.ForceUpdate {
				return nil, cmd, fmt.Errorf("conflicts detected: %s", plan.TargetPath)
			}
			return nil, cmd, nil
		}
		duplicatePlan = plan
	}
	if err := o.fs.MkdirAll(filepath.Dir(finalRoot), 0o755); err != nil {
		return nil, cmd, fmt.Errorf("create artifact staging parent: %w", err)
	}
	root, err := afero.TempDir(o.fs, filepath.Dir(finalRoot), ".javinizer-apply-")
	if err != nil {
		return nil, cmd, fmt.Errorf("create artifact staging area: %w", err)
	}
	stage := &artifactStage{fs: o.fs, fencer: artifactFencer(cmd), root: root, finalRoot: finalRoot, sourcePath: cmd.Match.Path, inPlace: inPlace, original: cmd, duplicatePlan: duplicatePlan}
	stagedCmd := cmd
	if duplicatePlan != nil {
		stagedCmd.Organize.DuplicateTracker = nil
	}
	if !cmd.Organize.Skip {
		if sourcePath == "" {
			stage.cleanup()
			return nil, cmd, fmt.Errorf("artifact staging requires a source path")
		}
		sourceInfo, statErr := o.fs.Stat(sourcePath)
		if statErr != nil {
			stage.cleanup()
			return nil, cmd, fmt.Errorf("artifact staging source: %w", statErr)
		}
		if !sourceInfo.Mode().IsRegular() {
			stage.cleanup()
			return nil, cmd, fmt.Errorf("artifact staging blocked for non-regular source %s", cmd.Match.Path)
		}
		base := filepath.Base(sourcePath)
		stagedDir := filepath.Join(root, ".source")
		if inPlace {
			stagedDir = root
		}
		stage.stagedSource = filepath.Join(stagedDir, base)
		if err := copyArtifactFile(o.fs, sourcePath, stage.stagedSource, sourceInfo.Mode().Perm()); err != nil {
			stage.cleanup()
			return nil, cmd, err
		}
		entries, readErr := afero.ReadDir(o.fs, filepath.Dir(sourcePath))
		if readErr != nil {
			stage.cleanup()
			return nil, cmd, fmt.Errorf("artifact staging source directory: %w", readErr)
		}
		for _, entry := range entries {
			if entry.Name() == base || entry.IsDir() || !isStagedArtifactSibling(base, entry.Name()) {
				continue
			}
			sibling := filepath.Join(filepath.Dir(sourcePath), entry.Name())
			siblingInfo, siblingErr := o.fs.Stat(sibling)
			if siblingErr != nil {
				stage.cleanup()
				return nil, cmd, fmt.Errorf("artifact staging sibling %s: %w", sibling, siblingErr)
			}
			if !siblingInfo.Mode().IsRegular() {
				continue
			}
			stagedSibling := filepath.Join(stagedDir, entry.Name())
			if err := copyArtifactFile(o.fs, sibling, stagedSibling, siblingInfo.Mode().Perm()); err != nil {
				stage.cleanup()
				return nil, cmd, err
			}
			stage.siblings = append(stage.siblings, artifactSibling{sourcePath: sibling, stagedPath: stagedSibling})
		}
		stagedCmd.Match.Path = stage.stagedSource
		stagedCmd.Match.Name = base
	}
	stagedCmd.DestPath = root
	return stage, stagedCmd, nil
}

func isStagedArtifactSibling(sourceName, siblingName string) bool {
	sourceStem := strings.TrimSuffix(strings.ToLower(sourceName), strings.ToLower(filepath.Ext(sourceName)))
	siblingExt := strings.ToLower(filepath.Ext(siblingName))
	siblingStem := strings.TrimSuffix(strings.ToLower(siblingName), siblingExt)
	if isStagedArtifactSubtitleExtension(siblingExt) {
		return siblingStem == sourceStem || isStagedArtifactPrefixedStem(sourceStem, siblingStem)
	}
	if !isStagedArtifactVideoExtension(siblingExt) {
		return false
	}
	if stagedArtifactMultipartRoot(sourceStem) != "" {
		return false
	}
	sourcePart := stagedArtifactMultipartRoot(sourceStem)
	siblingPart := stagedArtifactMultipartRoot(siblingStem)
	return siblingPart == sourceStem || sourcePart == siblingStem || (sourcePart != "" && sourcePart == siblingPart)
}

func isStagedArtifactPrefixedStem(sourceStem, siblingStem string) bool {
	if !strings.HasPrefix(siblingStem, sourceStem) || len(siblingStem) == len(sourceStem) {
		return false
	}
	separator := siblingStem[len(sourceStem)]
	return separator == '.' || separator == '-' || separator == '_'
}

func isStagedArtifactSubtitleExtension(ext string) bool {
	switch ext {
	case ".srt", ".ass", ".ssa", ".sub", ".idx", ".sup", ".vtt", ".smi", ".sami":
		return true
	default:
		return false
	}
}

func isStagedArtifactVideoExtension(ext string) bool {
	switch ext {
	case ".mp4", ".mkv", ".avi", ".wmv", ".flv", ".mov", ".m4v", ".webm", ".mpg", ".mpeg", ".m2ts", ".ts":
		return true
	default:
		return false
	}
}

func stagedArtifactMultipartRoot(stem string) string {
	lower := strings.ToLower(stem)
	markers := []string{"-cd", ".cd", "_cd", "-disc", ".disc", "_disc", "-part", ".part", "_part", "-pt", ".pt", "_pt"}
	for _, marker := range markers {
		index := strings.LastIndex(lower, marker)
		if index <= 0 {
			continue
		}
		suffix := lower[index+len(marker):]
		if suffix == "" {
			continue
		}
		digits := true
		for _, r := range suffix {
			if r < '0' || r > '9' {
				digits = false
				break
			}
		}
		if digits {
			return lower[:index]
		}
	}
	return ""
}
func copyArtifactFile(fs afero.Fs, source, target string, mode os.FileMode) error {
	if err := fs.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create staged source directory: %w", err)
	}
	in, err := fs.Open(source)
	if err != nil {
		return fmt.Errorf("open artifact source: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := fs.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create staged source: %w", err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = fs.Remove(target)
		return fmt.Errorf("stage artifact source: %w", copyErr)
	}
	if closeErr != nil {
		_ = fs.Remove(target)
		return fmt.Errorf("close staged source: %w", closeErr)
	}
	return nil
}

func (s *artifactStage) cleanup() {
	if s == nil || s.fs == nil || s.root == "" {
		return
	}
	_ = s.fs.RemoveAll(s.root)
}

func (s *artifactStage) finalPath(path string) (string, error) {
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(s.root, cleanPath)
	if err != nil {
		return "", fmt.Errorf("staged path escapes staging area: %s", path)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if s.inPlace {
			parentRel, parentErr := filepath.Rel(filepath.Dir(s.finalRoot), cleanPath)
			if parentErr == nil && parentRel != ".." && !strings.HasPrefix(parentRel, ".."+string(filepath.Separator)) {
				return cleanPath, nil
			}
		}
		return "", fmt.Errorf("staged path escapes staging area: %s", path)
	}
	if rel == "." {
		return s.finalRoot, nil
	}
	return filepath.Join(s.finalRoot, rel), nil
}

func (s *artifactStage) mergeMatch(state *applyPipelineState, match models.FileMatchInfo) (models.FileMatchInfo, error) {
	path := s.finalRoot
	if state.organizeResult != nil && state.organizeResult.NewPath != "" {
		mapped, err := s.finalPath(state.organizeResult.NewPath)
		if err != nil {
			return match, err
		}
		path = mapped
	} else if match.Name != "" {
		path = filepath.Join(path, match.Name)
	}
	match.Path = path
	return match, nil
}

func (s *artifactStage) finishDuplicateClaim(err error) {
	plan := s.duplicatePlan
	if plan == nil {
		return
	}
	if tracker := s.original.Organize.DuplicateTracker; tracker != nil {
		if err == nil || fsutil.PublishCompleted(err) {
			tracker.SettleClaim(plan.SourcePath, plan.TargetPath)
		} else {
			tracker.ReleaseClaim(plan.SourcePath, plan.TargetPath)
		}
	}
	s.duplicatePlan = nil
}

func (s *artifactStage) publish(ctx context.Context, o *applyOrchImpl, state *applyPipelineState, steps *stepCompletion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.original.Movie == nil || strings.TrimSpace(s.original.Movie.ContentID) == "" {
		return fmt.Errorf("artifact publication requires movie content id")
	}
	returnErr := s.fencer.WithApplyArtifactPublicationFence(ctx, s.original.Movie.ContentID, s.original.Movie.RenderGeneration, func(authoritative *models.Movie) error {
		if authoritative == nil || authoritative.RenderGeneration != s.original.Movie.RenderGeneration {
			return fmt.Errorf("artifact publication authoritative movie changed")
		}
		return s.publishUnderFence(ctx, o, state, steps)
	})
	// Only a never-persisted movie may use the legacy publication path.
	if errors.Is(returnErr, database.ErrNotFound) && !s.original.PersistedMovie {
		returnErr = s.publishUnderFence(ctx, o, state, steps)
	}
	s.finishDuplicateClaim(returnErr)
	if errors.Is(returnErr, database.ErrApplyPublicationStale) {
		s.rejected = true
		s.reject(state)
		return nil
	}
	return returnErr
}

func (s *artifactStage) reject(state *applyPipelineState) {
	state.organizeResult = nil
	state.downloadPaths = nil
	state.nfoPath = ""
	state.finalDir = s.finalRoot
	state.targetDir = s.finalRoot
}

func (s *artifactStage) publishUnderFence(ctx context.Context, o *applyOrchImpl, state *applyPipelineState, steps *stepCompletion) error {
	var finalResult *organizer.OrganizeResult
	stagedVideo := ""
	if !s.original.Organize.Skip {
		if state.organizeResult == nil {
			return fmt.Errorf("artifact publication has no organize result")
		}
		if state.organizeResult.DuplicateSkipped {
			return nil
		}
		stagedVideo = state.organizeResult.NewPath
		if stagedVideo == "" {
			stagedVideo = s.stagedSource
		}
		executor, ok := o.organizer.(artifactPlanExecutor)
		if !ok {
			return fmt.Errorf("artifact publication blocked: organizer has no planned execution seam")
		}
		match := s.original.Match
		match.Path = stagedVideo
		if !s.original.Organize.MoveFiles && s.original.Organize.LinkMode != organizer.LinkModeNone {
			match.Path = s.sourcePath
		}
		match.Name = filepath.Base(match.Path)
		plan, err := executor.PlanOrganize(ctx, organizer.OrganizeCmd{Match: match, Movie: s.original.Movie, DestDir: s.finalRoot, ForceUpdate: s.original.Organize.ForceUpdate, MoveFiles: s.original.Organize.MoveFiles, LinkMode: s.original.Organize.LinkMode, OperationMode: s.original.OperationMode, ForceRenameFile: s.original.Organize.ForceRenameFile})
		if err != nil {
			return fmt.Errorf("replan artifact publication: %w", err)
		}
		if !executor.PlanSourceExists(plan) {
			return fmt.Errorf("artifact publication staged source disappeared: %s", stagedVideo)
		}
		finalResult, err = executor.ExecuteOrganizePlan(plan, s.original.Organize.MoveFiles || s.original.Organize.LinkMode == organizer.LinkModeNone, s.original.Organize.LinkMode)
		if err != nil {
			return fmt.Errorf("publish organized video: %w", err)
		}
		if finalResult == nil {
			return fmt.Errorf("publish organized video returned no result")
		}
		finalResult.OriginalPath = s.sourcePath
	}
	if err := s.rehomeRemainingSiblings(stagedVideo); err != nil {
		return err
	}
	artifactSkipDir := filepath.Dir(s.stagedSource)
	if s.inPlace {
		artifactSkipDir = ""
	}
	stagedArtifactDir := ""
	finalArtifactDir := ""
	if finalResult != nil && !s.inPlace {
		stagedArtifactDir = filepath.Dir(stagedVideo)
		finalArtifactDir = finalResult.FolderPath
		if finalArtifactDir == "" {
			finalArtifactDir = filepath.Dir(finalResult.NewPath)
		}
	}
	preservedMedia, err := s.installTree(stagedVideo, artifactSkipDir, state.downloadPaths, stagedArtifactDir, finalArtifactDir)
	if err != nil {
		return err
	}
	if preservedMedia && steps != nil {
		steps.PosterVerified = false
	}
	if !s.original.Organize.Skip && s.original.Organize.MoveFiles && s.sourcePath != "" && filepath.Clean(s.sourcePath) != filepath.Clean(finalResult.NewPath) {
		// Planned execution publishes the video, but may leave copied siblings
		// under .source. Publish them before removing any original sidecar.
		for _, sibling := range s.siblings {
			target := filepath.Join(filepath.Dir(finalResult.NewPath), stagedArtifactSiblingName(filepath.Base(s.sourcePath), filepath.Base(finalResult.NewPath), filepath.Base(sibling.sourcePath)))
			if _, err := s.fs.Stat(target); os.IsNotExist(err) {
				info, statErr := s.fs.Stat(sibling.stagedPath)
				if statErr != nil {
					return fmt.Errorf("inspect staged publication sidecar: %w", statErr)
				}
				if copyErr := copyArtifactFile(s.fs, sibling.stagedPath, target, info.Mode().Perm()); copyErr != nil {
					return fmt.Errorf("publish sidecar before source cleanup: %w", copyErr)
				}
			} else if err != nil {
				return fmt.Errorf("inspect publication sidecar: %w", err)
			}
		}
		if err := s.fs.Remove(s.sourcePath); err != nil {
			return fmt.Errorf("remove original after artifact publication: %w", err)
		}
		for _, sibling := range s.siblings {
			if err := s.fs.Remove(sibling.sourcePath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove original sidecar after artifact publication: %w", err)
			}
		}
		if s.inPlace && state.organizeResult != nil && state.organizeResult.InPlaceRenamed && finalResult.FolderPath != "" {
			oldDir := filepath.Dir(s.sourcePath)
			if filepath.Clean(oldDir) != filepath.Clean(finalResult.FolderPath) {
				if entries, readErr := afero.ReadDir(s.fs, oldDir); readErr == nil && len(entries) == 0 {
					_ = s.fs.Remove(oldDir)
				}
			}
		}
	}
	if finalResult != nil {
		if s.inPlace {
			if finalResult.NewPath != "" {
				if mapped, mapErr := s.finalPath(finalResult.NewPath); mapErr == nil {
					finalResult.NewPath = mapped
				}
			}
			if finalResult.FolderPath != "" {
				if mapped, mapErr := s.finalPath(finalResult.FolderPath); mapErr == nil {
					finalResult.FolderPath = mapped
				}
			}
		}
		state.organizeResult = finalResult
		state.finalDir = finalResult.FolderPath
		state.targetDir = finalResult.FolderPath
	} else {
		state.finalDir = s.finalRoot
		state.targetDir = s.finalRoot
	}
	if state.nfoPath != "" {
		mapped, err := s.publicationPath(state.nfoPath, stagedArtifactDir, finalArtifactDir)
		if err != nil {
			return err
		}
		state.nfoPath = mapped
	}
	for i, path := range state.downloadPaths {
		mapped, err := s.publicationPath(path, stagedArtifactDir, finalArtifactDir)
		if err != nil {
			return err
		}
		state.downloadPaths[i] = mapped
	}
	return nil
}

func (s *artifactStage) rehomeRemainingSiblings(stagedVideo string) error {
	if stagedVideo == "" || s.original.Organize.LinkMode != organizer.LinkModeNone || s.stagedSource == "" {
		return nil
	}
	targetDir := filepath.Dir(stagedVideo)
	for _, sibling := range s.siblings {
		if _, err := s.fs.Stat(sibling.stagedPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect staged sidecar %s: %w", sibling.stagedPath, err)
		}
		target := filepath.Join(targetDir, stagedArtifactSiblingName(filepath.Base(s.stagedSource), filepath.Base(stagedVideo), filepath.Base(sibling.stagedPath)))
		if filepath.Clean(target) == filepath.Clean(sibling.stagedPath) {
			continue
		}
		if err := s.fs.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create staged sidecar directory: %w", err)
		}
		if _, err := s.fs.Stat(target); err == nil {
			if err := s.fs.Remove(target); err != nil {
				return fmt.Errorf("replace staged sidecar %s: %w", target, err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect staged sidecar target %s: %w", target, err)
		}
		if err := s.fs.Rename(sibling.stagedPath, target); err != nil {
			return fmt.Errorf("stage sidecar %s: %w", target, err)
		}
	}
	return nil
}

func stagedArtifactSiblingName(sourceName, targetName, siblingName string) string {
	sourceExt := filepath.Ext(sourceName)
	sourceStem := strings.TrimSuffix(sourceName, sourceExt)
	siblingExt := filepath.Ext(siblingName)
	siblingStem := strings.TrimSuffix(siblingName, siblingExt)
	targetStem := strings.TrimSuffix(targetName, filepath.Ext(targetName))
	if strings.EqualFold(siblingStem, sourceStem) {
		return targetStem + siblingExt
	}
	if len(siblingStem) > len(sourceStem) && strings.EqualFold(siblingStem[:len(sourceStem)], sourceStem) {
		separator := siblingStem[len(sourceStem)]
		if separator == '.' || separator == '-' || separator == '_' {
			return targetStem + siblingStem[len(sourceStem):] + siblingExt
		}
	}
	return siblingName
}

func (s *artifactStage) installTree(skipFile, skipDir string, preserve []string, stagedArtifactDir, finalArtifactDir string) (bool, error) {
	if s.inPlace {
		if _, err := s.fs.Stat(s.root); os.IsNotExist(err) {
			return false, nil
		} else if err != nil {
			return false, fmt.Errorf("inspect staged artifact root: %w", err)
		}
	}
	paths := make([]string, 0)
	if err := afero.Walk(s.fs, s.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		clean := filepath.Clean(path)
		if skipFile != "" && clean == filepath.Clean(skipFile) {
			return nil
		}
		if skipDir != "" {
			prefix := filepath.Clean(skipDir) + string(filepath.Separator)
			if strings.HasPrefix(clean, prefix) {
				return nil
			}
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return false, fmt.Errorf("walk staged artifacts: %w", err)
	}
	sort.Strings(paths)
	return s.installPaths(paths, preserve, stagedArtifactDir, finalArtifactDir)
}

func (s *artifactStage) installPaths(paths, preserve []string, stagedArtifactDir, finalArtifactDir string) (bool, error) {
	preserved := false
	for _, source := range paths {
		target, err := s.publicationPath(source, stagedArtifactDir, finalArtifactDir)
		if err != nil {
			return false, err
		}
		if !s.original.OverwriteExistingMedia && containsPath(preserve, source) {
			if _, statErr := s.fs.Stat(target); statErr == nil {
				preserved = true
				continue
			}
		}
		if err := s.fs.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return false, fmt.Errorf("create artifact destination: %w", err)
		}
		if info, statErr := s.fs.Stat(target); statErr == nil {
			if info.IsDir() {
				return false, fmt.Errorf("artifact destination is a directory: %s", target)
			}
			if removeErr := s.fs.Remove(target); removeErr != nil {
				return false, fmt.Errorf("replace artifact destination %s: %w", target, removeErr)
			}
		} else if !os.IsNotExist(statErr) {
			return false, fmt.Errorf("inspect artifact destination %s: %w", target, statErr)
		}
		if err := s.fs.Rename(source, target); err != nil {
			return false, fmt.Errorf("publish staged artifact %s: %w", target, err)
		}
	}
	return preserved, nil
}

func (s *artifactStage) publicationPath(path, stagedArtifactDir, finalArtifactDir string) (string, error) {
	target, err := s.finalPath(path)
	if err != nil {
		return "", err
	}
	if stagedArtifactDir == "" || finalArtifactDir == "" {
		return target, nil
	}
	rel, _ := filepath.Rel(stagedArtifactDir, path)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return target, nil
	}
	return filepath.Join(finalArtifactDir, rel), nil
}

func containsPath(paths []string, path string) bool {
	clean := filepath.Clean(path)
	for _, candidate := range paths {
		if filepath.Clean(candidate) == clean {
			return true
		}
	}
	return false
}
