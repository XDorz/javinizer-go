package workflow

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/javinizer/javinizer-go/internal/operationmode"
	"github.com/javinizer/javinizer-go/internal/organizer"
	"github.com/javinizer/javinizer-go/internal/template"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

type pr260PublicationFaultOrganizer struct {
	*organizer.Organizer
	planCalls     int
	failPlanAt    int
	failExecute   bool
	noResult      bool
	missingSource bool
}

func (o *pr260PublicationFaultOrganizer) PlanOrganize(ctx context.Context, cmd organizer.OrganizeCmd) (*organizer.OrganizePlan, error) {
	o.planCalls++
	if o.planCalls == o.failPlanAt {
		return nil, errors.New("publication plan denied")
	}
	return o.Organizer.PlanOrganize(ctx, cmd)
}
func (o *pr260PublicationFaultOrganizer) PlanSourceExists(p *organizer.OrganizePlan) bool {
	if o.missingSource {
		return false
	}
	return o.Organizer.PlanSourceExists(p)
}
func (o *pr260PublicationFaultOrganizer) ExecuteOrganizePlan(p *organizer.OrganizePlan, move bool, link organizer.LinkMode) (*organizer.OrganizeResult, error) {
	if o.failExecute {
		return nil, errors.New("final execution denied")
	}
	if o.noResult {
		return nil, nil
	}
	return o.Organizer.ExecuteOrganizePlan(p, move, link)
}

func TestPR260PublicationClaimPlanAndExecutionFaults(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		failPlanAt                           int
		failExecute, noResult, missingSource bool
		want                                 string
		claim                                bool
	}{
		{name: "plan before claim", failPlanAt: 1, want: "plan artifact duplicate claim"},
		{name: "replan after claim", failPlanAt: 2, want: "replan artifact publication", claim: true},
		{name: "missing staged source", missingSource: true, want: "staged source disappeared", claim: true},
		{name: "final execution", failExecute: true, want: "final execution denied", claim: true},
		{name: "nil final result", noResult: true, want: "returned no result", claim: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, _ := pr260ArtifactDB(t)
			movie := pr260FencedMovie(t, db, "claim-"+tc.name, "")
			fs, root, source, subtitle, multipart, unrelated, match := pr260FencedFiles(t, "claim-"+tc.name)
			dest := filepath.Join(root, "library")
			real := organizer.NewOrganizer(fs, &organizer.Config{FolderFormat: "shared", FileFormat: "shared", RenameFile: true, OperationMode: operationmode.OperationModeOrganize}, template.NewEngine(), nil)
			fault := &pr260PublicationFaultOrganizer{Organizer: real, failPlanAt: tc.failPlanAt, failExecute: tc.failExecute, noResult: tc.noResult, missingSource: tc.missingSource}
			orch := &applyOrchImpl{fs: fs, organizer: fault}
			tracker := organizer.NewDuplicateTracker(false)
			cmd := pr260ArtifactFailureCommand(&movie, match, dest)
			cmd.Download = false
			cmd.Organize.Skip = false
			cmd.Organize.MoveFiles = true
			cmd.Organize.ForceUpdate = true
			cmd.Organize.DuplicateTracker = tracker
			cmd.PublicationFence = pr260FencedCounter(t, db)
			stage, staged, err := orch.prepareArtifact(context.Background(), cmd)
			if !tc.claim {
				require.ErrorContains(t, err, tc.want)
				require.Nil(t, stage)
			} else {
				require.NoError(t, err)
				require.NotNil(t, stage)
				require.Nil(t, staged.Organize.DuplicateTracker, "only original command owns the claim")
				require.NotNil(t, stage.duplicatePlan)
				target := stage.duplicatePlan.TargetPath
				state := &applyPipelineState{organizeResult: &organizer.OrganizeResult{NewPath: stage.stagedSource}}
				err = stage.publish(context.Background(), orch, state, nil)
				require.ErrorContains(t, err, tc.want)
				other := filepath.Join(root, "incoming", "other.mp4")
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				done := make(chan bool, 1)
				go func() { _, duplicate := tracker.ObserveClaim(ctx, other, target, true); done <- duplicate }()
				select {
				case duplicate := <-done:
					require.False(t, duplicate, "failed publication must release claim")
				case <-ctx.Done():
					t.Fatal("claim waiter blocked")
				}
				tracker.ReleaseClaim(other, target)
				exists, e := afero.Exists(fs, stage.stagedSource)
				require.NoError(t, e)
				require.True(t, exists, "fault leaves owned stage for cleanup")
				stage.cleanup()
			}
			pr260AssertRetained(t, fs, source, subtitle, multipart, unrelated)
			pr260AssertNoFinals(t, fs, dest)
			pr260AssertStageGone(t, fs, root)
		})
	}
}
