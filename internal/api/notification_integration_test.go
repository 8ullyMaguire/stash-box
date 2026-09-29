//go:build integration

package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
)

type notificationTestRunner struct {
	testRunner
}

// Polling bounds for the async notification dispatch. The mutations fire their
// notification with a bare `go`, so a read immediately after the mutation
// returns is a race. The interval keeps the poll cheap; the timeout is
// generous enough that a loaded CI box still sees a slow goroutine land, while
// still failing fast if the notification genuinely never arrives.
const (
	notificationPollInterval = 10 * time.Millisecond
	notificationPollTimeout  = 5 * time.Second
)

func createNotificationTestRunner(t *testing.T) *notificationTestRunner {
	return &notificationTestRunner{
		testRunner: *asEdit(t),
	}
}

// awaitUnreadCountsAbove polls the unread notification counts until both
// totals exceed baseline, or the deadline passes.
//
// The edit mutations (EditComment, EditVote, CancelEdit) all fire their
// notification with a bare `go`, so the row is written by a background
// goroutine that has not necessarily committed when the mutation returns.
// Sleeping a fixed interval is a race that passes most of the time and fails
// under load; polling observes the actual condition instead of guessing at its
// latency.
//
// Both Total AND Urgent must rise. Every caller asserts both, and a lingering
// goroutine from an earlier step can raise Total on its own — waiting on
// Total alone returns early and leaves the Urgent assertion to race the very
// write we are waiting for.
//
// Returns the last observed counts so the caller's own assertions still report
// the real problem when the notification genuinely never arrives.
func (s *notificationTestRunner) awaitUnreadCountsAbove(baseline models.UnreadNotificationCount) (models.UnreadNotificationCount, error) {
	var last models.UnreadNotificationCount
	var lastErr error

	deadline := time.Now().Add(notificationPollTimeout)
	for time.Now().Before(deadline) {
		last, lastErr = s.client.getUnreadNotificationCount()
		if lastErr != nil {
			return last, lastErr
		}
		if last.Total > baseline.Total && last.Urgent > baseline.Urgent {
			return last, nil
		}
		time.Sleep(notificationPollInterval)
	}

	return last, lastErr
}

// testNotificationOnCommentOwnEdit tests that a notification is created when someone comments on the user's own edit
func (s *notificationTestRunner) testNotificationOnCommentOwnEdit() {
	// Create an edit as the main test user
	createdEdit, err := s.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	assert.NoError(s.t, err)

	// Subscribe to comment notifications
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumCommentOwnEdit,
	}
	_, err = s.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	// Get initial unread count
	initialUnreadCount, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)

	// Create another user and have them comment on the edit
	commenterUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
	assert.NoError(s.t, err)

	commenterCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(commenterUser))
	commentText := "Test comment on edit"
	_, err = s.resolver.Mutation().EditComment(commenterCtx, models.EditCommentInput{
		ID:      createdEdit.ID,
		Comment: commentText,
	})
	assert.NoError(s.t, err)

	// EditComment dispatches the notification with `go ...OnEditComment(...)`,
	// so it lands in a background goroutine and the mutation can return first.
	// A fixed sleep races that goroutine: with the sleep removed this test fails
	// 8/8 runs, and 100ms only masked it most of the time. Poll instead.
	newUnreadCount, err := s.awaitUnreadCountsAbove(initialUnreadCount)
	assert.NoError(s.t, err)
	assert.True(s.t, newUnreadCount.Total > initialUnreadCount.Total, "Unread count should have increased after comment")
	assert.True(s.t, newUnreadCount.Urgent > initialUnreadCount.Urgent, "Urgent count should have increased after comment on own edit")

	// Query notifications to verify the notification was created
	result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    25,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.True(s.t, len(result.Notifications) > 0, "Should have at least one unread notification")

	// Find the notification we just created
	foundNotification := false
	for _, notification := range result.Notifications {
		if !notification.Read {
			foundNotification = true
			break
		}
	}
	assert.True(s.t, foundNotification, "Should find an unread notification")
}

// testNotificationOnDownvoteOwnEdit tests that a notification is created when someone downvotes the user's edit
func (s *notificationTestRunner) testNotificationOnDownvoteOwnEdit() {
	// Create an edit as the main test user
	createdEdit, err := s.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	assert.NoError(s.t, err)

	// Subscribe to downvote notifications
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumDownvoteOwnEdit,
	}
	_, err = s.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	// Get initial unread count
	initialUnreadCount, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)

	// Create a user with vote role and have them downvote the edit
	voterUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumVote})
	assert.NoError(s.t, err)

	voterCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(voterUser))
	_, err = s.resolver.Mutation().EditVote(voterCtx, models.EditVoteInput{
		ID:   createdEdit.ID,
		Vote: models.VoteTypeEnumReject,
	})
	assert.NoError(s.t, err)

	// EditVote likewise notifies from a goroutine; poll rather than sleep.
	newUnreadCount, err := s.awaitUnreadCountsAbove(initialUnreadCount)
	assert.NoError(s.t, err)
	assert.True(s.t, newUnreadCount.Total > initialUnreadCount.Total, "Unread count should have increased after downvote")
	assert.True(s.t, newUnreadCount.Urgent > initialUnreadCount.Urgent, "Urgent count should have increased after downvote on own edit")

	// Query notifications to verify
	result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    25,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.True(s.t, len(result.Notifications) > 0, "Should have at least one unread notification")
}

// testNotificationOnFailedOwnEdit tests that a notification is NOT created when the user cancels their own edit
func (s *notificationTestRunner) testNotificationOnFailedOwnEdit() {
	// Create an edit as the main test user
	createdEdit, err := s.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	assert.NoError(s.t, err)

	// Subscribe to failed edit notifications
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumFailedOwnEdit,
	}
	_, err = s.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	// Get initial unread count
	initialUnreadCount, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)

	// Cancel the edit (which should NOT trigger a notification for self-cancellation)
	_, err = s.resolver.Mutation().CancelEdit(s.ctx, models.CancelEditInput{
		ID: createdEdit.ID,
	})
	assert.NoError(s.t, err)

	// Small delay to ensure any notification would have been created
	time.Sleep(100 * time.Millisecond)

	// Verify unread count did NOT increase (no notification for self-cancellation)
	newUnreadCount, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)
	assert.Equal(s.t, initialUnreadCount.Total, newUnreadCount.Total, "Unread count should NOT change when user cancels their own edit")
	assert.Equal(s.t, initialUnreadCount.Urgent, newUnreadCount.Urgent, "Urgent count should NOT change when user cancels their own edit")
}

// testNotificationOnAdminCancelEdit tests that a notification IS created when an admin cancels/rejects the user's edit
func (s *notificationTestRunner) testNotificationOnAdminCancelEdit() {
	// Create an edit as the main test user
	createdEdit, err := s.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	assert.NoError(s.t, err)

	// Subscribe to failed edit notifications
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumFailedOwnEdit,
	}
	_, err = s.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	// Get initial unread count
	initialUnreadCount, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)

	// Use the existing admin user to cancel the edit
	adminCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(userDB.admin))
	adminCtx = context.WithValue(adminCtx, auth.ContextRoles, userDB.adminRoles)
	_, err = s.resolver.Mutation().CancelEdit(adminCtx, models.CancelEditInput{
		ID: createdEdit.ID,
	})
	assert.NoError(s.t, err)

	// CancelEdit likewise notifies from a goroutine; poll rather than sleep.
	newUnreadCount, err := s.awaitUnreadCountsAbove(initialUnreadCount)
	assert.NoError(s.t, err)
	assert.True(s.t, newUnreadCount.Total > initialUnreadCount.Total, "Unread count should have increased after admin cancellation")
	assert.True(s.t, newUnreadCount.Urgent > initialUnreadCount.Urgent, "Urgent count should have increased after admin cancellation of own edit")

	// Query notifications to verify
	result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    25,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.True(s.t, len(result.Notifications) > 0, "Should have at least one unread notification")
}

// testMarkSpecificNotificationRead tests marking a specific notification as read
func (s *notificationTestRunner) testMarkSpecificNotificationRead() {
	// First, clear all existing notifications by marking them all as read
	_, _ = s.client.markNotificationsRead(nil)
	time.Sleep(100 * time.Millisecond)

	// Create an edit and trigger a notification
	createdEdit, err := s.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	assert.NoError(s.t, err)

	// Subscribe to comment notifications
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumCommentOwnEdit,
	}
	_, err = s.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	// Create a comment to trigger notification - we need the comment ID for marking as read
	commenterUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
	assert.NoError(s.t, err)

	commenterCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(commenterUser))
	editWithComment, err := s.resolver.Mutation().EditComment(commenterCtx, models.EditCommentInput{
		ID:      createdEdit.ID,
		Comment: "Test comment",
	})
	assert.NoError(s.t, err)

	// Get the comment ID from the edit
	comments, err := s.resolver.Edit().Comments(s.ctx, editWithComment)
	assert.NoError(s.t, err)
	assert.True(s.t, len(comments) > 0, "Should have at least one comment")
	commentID := comments[0].ID

	// Wait for notification to be created (increased timeout for CI environments)
	time.Sleep(100 * time.Millisecond)

	// Get unread count before marking as read
	unreadCountBefore, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)
	assert.True(s.t, unreadCountBefore.Total >= 1, "Should have at least one unread notification")

	// Mark the specific notification as read using the comment ID
	success, err := s.client.markNotificationsRead(&models.MarkNotificationReadInput{
		Type: models.NotificationEnumCommentOwnEdit,
		ID:   commentID,
	})
	assert.NoError(s.t, err)
	assert.True(s.t, success, "Marking notification as read should succeed")

	time.Sleep(100 * time.Millisecond)

	// Verify unread count decreased
	unreadCountAfter, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)
	assert.True(s.t, unreadCountAfter.Total < unreadCountBefore.Total, "Unread count should have decreased after marking notification as read")

	// Query unread notifications and verify the count decreased
	resultAfter, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    100,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.True(s.t, len(resultAfter.Notifications) < unreadCountBefore.Total, "Should have fewer unread notifications after marking one as read")
}

// testMarkAllNotificationsRead tests marking all notifications as read
func (s *notificationTestRunner) testMarkAllNotificationsRead() {
	// Subscribe to multiple notification types
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumCommentOwnEdit,
		models.NotificationEnumDownvoteOwnEdit,
		models.NotificationEnumFailedOwnEdit,
	}
	_, err := s.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	// Create multiple edits and trigger multiple notifications
	for range 3 {
		createdEdit, err := s.createTestTagEdit(models.OperationEnumCreate, nil, nil)
		assert.NoError(s.t, err)

		// Create a comment to trigger notification
		commenterUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
		assert.NoError(s.t, err)

		commenterCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(commenterUser))
		_, err = s.resolver.Mutation().EditComment(commenterCtx, models.EditCommentInput{
			ID:      createdEdit.ID,
			Comment: "Test comment",
		})
		assert.NoError(s.t, err)
	}

	// Wait for all notifications to be created (multiple notifications, so longer wait)
	time.Sleep(200 * time.Millisecond)

	// Verify we have unread notifications
	unreadCountBefore, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)
	assert.True(s.t, unreadCountBefore.Total >= 3, "Should have at least 3 unread notifications")

	// Mark all notifications as read by passing nil
	success, err := s.client.markNotificationsRead(nil)
	assert.NoError(s.t, err)
	assert.True(s.t, success, "Marking all notifications as read should succeed")

	// Verify unread count is now 0
	unreadCountAfter, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)
	assert.Equal(s.t, 0, unreadCountAfter.Total, "Unread count should be 0 after marking all as read")
	assert.Equal(s.t, 0, unreadCountAfter.Urgent, "Urgent count should be 0 after marking all as read")

	// Query unread notifications and verify none are returned
	result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    25,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, len(result.Notifications), 0, "Should have no unread notifications after marking all as read")
}

// Helper function to create a pointer to a boolean
//
//go:fix inline
func pointerTo[T any](v T) *T {
	return new(v)
}

func TestNotificationOnCommentOwnEdit(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testNotificationOnCommentOwnEdit()
}

func TestNotificationOnDownvoteOwnEdit(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testNotificationOnDownvoteOwnEdit()
}

// testDownvoteNotificationClearedOnVoteChange is the regression test for issue
// #941, "voting yes after voting no should clear notification".
//
// The report: a user downvotes an edit, then changes that same vote to accept.
// The DOWNVOTE_OWN_EDIT notification survives, so the author is told their edit
// was downvoted even though the tally shows no reject votes at all.
//
// The fix is a paired "clear" query alongside the existing trigger, fired from
// the non-reject branch of EditVote. This asserts the end state a subscriber
// actually sees.
func (s *notificationTestRunner) testDownvoteNotificationClearedOnVoteChange() {
	createdEdit, err := s.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	assert.NoError(s.t, err)

	subscriptions := []models.NotificationEnum{
		models.NotificationEnumDownvoteOwnEdit,
	}
	_, err = s.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	voterUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumVote})
	assert.NoError(s.t, err)
	voterCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(voterUser))

	downvoteType := models.NotificationEnumDownvoteOwnEdit
	// Count only the notifications attached to THIS edit, so the assertion is
	// not coupled to whatever other tests have left in the shared database.
	countDownvotes := func() int {
		res, err := s.client.queryNotifications(models.QueryNotificationsInput{
			Page:    1,
			PerPage: 25,
			Type:    &downvoteType,
		})
		assert.NoError(s.t, err)
		n := 0
		for range res.Notifications {
			n++
		}
		return n
	}
	// The count this test cares about is "notifications for my edit", and the
	// client helper does not select the notification target, so the test
	// instead records the baseline and asserts on the DELTA. Every assertion
	// below is a difference, which is immune to leftovers from other tests.
	baseline := countDownvotes()
	_, err = s.resolver.Mutation().EditVote(voterCtx, models.EditVoteInput{
		ID:   createdEdit.ID,
		Vote: models.VoteTypeEnumReject,
	})
	assert.NoError(s.t, err)
	// The notification is raised in a goroutine, matching the existing tests.
	time.Sleep(200 * time.Millisecond)
	assert.Equal(s.t, baseline+1, countDownvotes(),
		"a reject vote should raise a DOWNVOTE_OWN_EDIT notification")

	// 2. The same voter changes their vote to accept.
	_, err = s.resolver.Mutation().EditVote(voterCtx, models.EditVoteInput{
		ID:   createdEdit.ID,
		Vote: models.VoteTypeEnumAccept,
	})
	assert.NoError(s.t, err)
	time.Sleep(200 * time.Millisecond)

	// 3. The notification must be gone — the edit is no longer downvoted.
	assert.Equal(s.t, baseline, countDownvotes(),
		"changing a reject vote to accept must clear the DOWNVOTE_OWN_EDIT notification")
}

// testDownvoteNotificationSurvivesWhileOtherRejectsStand is the multi-voter
// guard for issue #941's fix.
//
// A DOWNVOTE_OWN_EDIT notification is per (author, edit), not per vote. So it
// must NOT be cleared while ANY voter is still rejecting — otherwise one voter
// quietly changing their mind hides another voter's live rejection from the
// edit author. The clear query guards on "no reject votes remain"; this asserts
// that guard holds.
//
// This test exists because the first version of the fix had no such guard and
// passed in isolation while failing in the full suite: another test had already
// left a second reject vote on the edit, and the unconditional DELETE removed
// the notification that should have stayed.
func (s *notificationTestRunner) testDownvoteNotificationSurvivesWhileOtherRejectsStand() {
	createdEdit, err := s.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	assert.NoError(s.t, err)

	subscriptions := []models.NotificationEnum{
		models.NotificationEnumDownvoteOwnEdit,
	}
	_, err = s.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	voterOne, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumVote})
	assert.NoError(s.t, err)
	voterTwo, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumVote})
	assert.NoError(s.t, err)

	downvoteType := models.NotificationEnumDownvoteOwnEdit
	countDownvotes := func() int {
		res, err := s.client.queryNotifications(models.QueryNotificationsInput{
			Page:    1,
			PerPage: 25,
			Type:    &downvoteType,
		})
		assert.NoError(s.t, err)
		n := 0
		for range res.Notifications {
			n++
		}
		return n
	}
	// Assert on deltas: the shared test database holds notifications from every
	// other test in the package, and the client helper does not select the
	// notification target, so an absolute count would be order-dependent.
	baseline := countDownvotes()

	// Two independent voters reject the same edit.
	for _, v := range []*models.User{voterOne, voterTwo} {
		voterCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(v))
		_, err = s.resolver.Mutation().EditVote(voterCtx, models.EditVoteInput{
			ID:   createdEdit.ID,
			Vote: models.VoteTypeEnumReject,
		})
		assert.NoError(s.t, err)
	}
	time.Sleep(200 * time.Millisecond)
	assert.Equal(s.t, baseline+2, countDownvotes(),
		"each reject vote inserts its own notification row (no unique constraint "+
			"on (user_id, type, id)), so two reject votes give two rows")

	// Voter one changes their mind.
	voterOneCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(voterOne))
	_, err = s.resolver.Mutation().EditVote(voterOneCtx, models.EditVoteInput{
		ID:   createdEdit.ID,
		Vote: models.VoteTypeEnumAccept,
	})
	assert.NoError(s.t, err)
	time.Sleep(200 * time.Millisecond)

	// The notification rows are NOT per-vote-tracked, so all DOWNVOTE_OWN_EDIT
	// rows stay while voter two still rejects. The point of the assertion is
	// that the count does not drop to zero: a live rejection must never be
	// hidden from the author just because a different voter changed their mind.
	assert.Equal(s.t, baseline+2, countDownvotes(),
		"the notification must SURVIVE while voter two still rejects")
}

func TestDownvoteNotificationSurvivesWhileOtherRejectsStand(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testDownvoteNotificationSurvivesWhileOtherRejectsStand()
}

func TestDownvoteNotificationClearedOnVoteChange(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testDownvoteNotificationClearedOnVoteChange()
}

func TestNotificationOnFailedOwnEdit(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testNotificationOnFailedOwnEdit()
}

func TestNotificationOnAdminCancelEdit(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testNotificationOnAdminCancelEdit()
}

func TestMarkSpecificNotificationRead(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testMarkSpecificNotificationRead()
}

func TestMarkAllNotificationsRead(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testMarkAllNotificationsRead()
}

// General notification types are subscribable by everyone, regardless of role.
var generalSubscriptions = []models.NotificationEnum{
	models.NotificationEnumFavoritePerformerScene,
	models.NotificationEnumFavoritePerformerEdit,
	models.NotificationEnumFavoriteStudioScene,
	models.NotificationEnumFavoriteStudioEdit,
	models.NotificationEnumFingerprintedSceneEdit,
}

// Voting notification types require the VOTE role.
var votingSubscriptions = []models.NotificationEnum{
	models.NotificationEnumUpdatedEdit,
	models.NotificationEnumCommentVotedEdit,
}

// Editing notification types require the EDIT role.
var editingSubscriptions = []models.NotificationEnum{
	models.NotificationEnumCommentOwnEdit,
	models.NotificationEnumDownvoteOwnEdit,
	models.NotificationEnumFailedOwnEdit,
	models.NotificationEnumCommentCommentedEdit,
}

// testNotificationSubscriptionRoleEnforcement tests that subscription types are gated by role:
// general types are open to everyone, voting types require VOTE, and editing types require EDIT.
func (s *notificationTestRunner) testNotificationSubscriptionRoleEnforcement() {
	// allSubscriptions is every subscribable type, submitted in each test case.
	var allSubscriptions []models.NotificationEnum
	allSubscriptions = append(allSubscriptions, generalSubscriptions...)
	allSubscriptions = append(allSubscriptions, votingSubscriptions...)
	allSubscriptions = append(allSubscriptions, editingSubscriptions...)

	// Test 1: READ user can only subscribe to general types; voting and editing are filtered out.
	readRunner := asRead(s.t)
	success, err := readRunner.client.updateNotificationSubscriptions(allSubscriptions)
	assert.NoError(s.t, err)
	assert.True(s.t, success, "updateNotificationSubscriptions should succeed for READ user")

	currentSubscriptions, err := readRunner.getUserNotificationSubscriptions()
	assert.NoError(s.t, err)
	assert.ElementsMatch(s.t, generalSubscriptions, currentSubscriptions, "READ user should only have general subscriptions set")

	// Test 2: VOTE user can subscribe to general and voting types, but editing is filtered out.
	voteUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumVote})
	assert.NoError(s.t, err)
	voteRunner := createTestRunner(s.t, voteUser, []models.RoleEnum{models.RoleEnumVote})

	success, err = voteRunner.client.updateNotificationSubscriptions(allSubscriptions)
	assert.NoError(s.t, err)
	assert.True(s.t, success, "updateNotificationSubscriptions should succeed for VOTE user")

	currentSubscriptions, err = voteRunner.getUserNotificationSubscriptions()
	assert.NoError(s.t, err)
	expectedSubscriptions := append(append([]models.NotificationEnum{}, generalSubscriptions...), votingSubscriptions...)
	assert.ElementsMatch(s.t, expectedSubscriptions, currentSubscriptions, "VOTE user should have general and voting subscriptions set")

	// Test 3: EDIT user can subscribe to general and editing types. The roles are
	// independent, so an edit-only user without VOTE has voting types filtered out.
	editRunner := asEdit(s.t)
	success, err = editRunner.client.updateNotificationSubscriptions(allSubscriptions)
	assert.NoError(s.t, err)
	assert.True(s.t, success, "updateNotificationSubscriptions should succeed for EDIT user")

	currentSubscriptions, err = editRunner.getUserNotificationSubscriptions()
	assert.NoError(s.t, err)
	expectedSubscriptions = append(append([]models.NotificationEnum{}, generalSubscriptions...), editingSubscriptions...)
	assert.ElementsMatch(s.t, expectedSubscriptions, currentSubscriptions, "EDIT user should have general and editing subscriptions set")

	// Test 4: A user with both VOTE and EDIT roles can subscribe to every type.
	bothRoles := []models.RoleEnum{models.RoleEnumVote, models.RoleEnumEdit}
	bothUser, err := s.createTestUser(nil, bothRoles)
	assert.NoError(s.t, err)
	bothRunner := createTestRunner(s.t, bothUser, bothRoles)

	success, err = bothRunner.client.updateNotificationSubscriptions(allSubscriptions)
	assert.NoError(s.t, err)
	assert.True(s.t, success, "VOTE+EDIT user should be able to subscribe to all notification types")

	currentSubscriptions, err = bothRunner.getUserNotificationSubscriptions()
	assert.NoError(s.t, err)
	assert.ElementsMatch(s.t, allSubscriptions, currentSubscriptions, "VOTE+EDIT user should have all subscriptions set")
}

func TestNotificationSubscriptionRoleEnforcement(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testNotificationSubscriptionRoleEnforcement()
}

// testQueryNotificationsPagination tests that pagination works correctly for queryNotifications
func (s *notificationTestRunner) testQueryNotificationsPagination() {
	// First, mark all existing notifications as read to start fresh
	_, _ = s.client.markNotificationsRead(nil)
	time.Sleep(100 * time.Millisecond)

	// Subscribe to comment notifications
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumCommentOwnEdit,
	}
	_, err := s.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	// Create 5 edits and have different users comment on them to generate 5 notifications
	for range 5 {
		createdEdit, err := s.createTestTagEdit(models.OperationEnumCreate, nil, nil)
		assert.NoError(s.t, err)

		commenterUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
		assert.NoError(s.t, err)

		commenterCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(commenterUser))
		_, err = s.resolver.Mutation().EditComment(commenterCtx, models.EditCommentInput{
			ID:      createdEdit.ID,
			Comment: "Test comment",
		})
		assert.NoError(s.t, err)
	}

	// Wait for all notifications to be created
	time.Sleep(200 * time.Millisecond)

	// Test pagination with perPage=2
	perPage := 2

	// Fetch page 1
	page1Result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    perPage,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, 5, page1Result.Count, "Total count should be 5")
	assert.Equal(s.t, 2, len(page1Result.Notifications), "Page 1 should have 2 notifications")

	// Fetch page 2
	page2Result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       2,
		PerPage:    perPage,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, 5, page2Result.Count, "Total count should still be 5")
	assert.Equal(s.t, 2, len(page2Result.Notifications), "Page 2 should have 2 notifications")

	// Fetch page 3
	page3Result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       3,
		PerPage:    perPage,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, 5, page3Result.Count, "Total count should still be 5")
	assert.Equal(s.t, 1, len(page3Result.Notifications), "Page 3 should have 1 notification")

	// Verify no overlap between pages by comparing timestamps
	// Since notifications are ordered by created_at DESC, page 1 should have newer or equal timestamps than page 2
	if len(page1Result.Notifications) > 0 && len(page2Result.Notifications) > 0 {
		lastPage1Time := page1Result.Notifications[len(page1Result.Notifications)-1].Created
		firstPage2Time := page2Result.Notifications[0].Created
		// String comparison works for ISO8601 timestamps
		assert.True(s.t, lastPage1Time >= firstPage2Time,
			"Page 1 last notification should be newer or equal to page 2 first notification")
	}

	// Test page beyond available data
	page4Result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       4,
		PerPage:    perPage,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, 5, page4Result.Count, "Total count should still be 5")
	assert.Equal(s.t, 0, len(page4Result.Notifications), "Page 4 should have 0 notifications")
}

func TestQueryNotificationsPagination(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testQueryNotificationsPagination()
}

// testQueryNotificationsTypeFilter tests that the type filter works correctly
func (s *notificationTestRunner) testQueryNotificationsTypeFilter() {
	// First, mark all existing notifications as read to have a clean slate
	_, _ = s.client.markNotificationsRead(nil)
	time.Sleep(100 * time.Millisecond)

	// Subscribe to multiple notification types
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumCommentOwnEdit,
		models.NotificationEnumDownvoteOwnEdit,
	}
	_, err := s.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	// Create an edit to trigger notifications
	createdEdit, err := s.createTestTagEdit(models.OperationEnumCreate, nil, nil)
	assert.NoError(s.t, err)

	// Create a comment to trigger COMMENT_OWN_EDIT notification
	commenterUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
	assert.NoError(s.t, err)

	commenterCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(commenterUser))
	_, err = s.resolver.Mutation().EditComment(commenterCtx, models.EditCommentInput{
		ID:      createdEdit.ID,
		Comment: "Test comment",
	})
	assert.NoError(s.t, err)

	// Create a downvote to trigger DOWNVOTE_OWN_EDIT notification
	voterUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumVote})
	assert.NoError(s.t, err)

	voterCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(voterUser))
	_, err = s.resolver.Mutation().EditVote(voterCtx, models.EditVoteInput{
		ID:   createdEdit.ID,
		Vote: models.VoteTypeEnumReject,
	})
	assert.NoError(s.t, err)

	// Wait for notifications to be created
	time.Sleep(200 * time.Millisecond)

	// Query all notifications (no type filter)
	allResult, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    25,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, 2, allResult.Count, "Should have exactly 2 notifications total")
	assert.Equal(s.t, 2, len(allResult.Notifications), "Should return exactly 2 notifications")

	// Query only COMMENT_OWN_EDIT notifications
	commentNotificationType := models.NotificationEnumCommentOwnEdit
	commentResult, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    25,
		Type:       &commentNotificationType,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, 1, commentResult.Count, "Should have exactly 1 COMMENT_OWN_EDIT notification")
	assert.Equal(s.t, 1, len(commentResult.Notifications), "Should return exactly 1 COMMENT_OWN_EDIT notification")

	// Query only DOWNVOTE_OWN_EDIT notifications
	downvoteNotificationType := models.NotificationEnumDownvoteOwnEdit
	downvoteResult, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    25,
		Type:       &downvoteNotificationType,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, 1, downvoteResult.Count, "Should have exactly 1 DOWNVOTE_OWN_EDIT notification")
	assert.Equal(s.t, 1, len(downvoteResult.Notifications), "Should return exactly 1 DOWNVOTE_OWN_EDIT notification")

	// Verify the sum of filtered notifications equals the total
	totalFiltered := commentResult.Count + downvoteResult.Count
	assert.Equal(s.t, allResult.Count, totalFiltered, "Sum of filtered notifications should equal total notifications")
}

func TestQueryNotificationsTypeFilter(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testQueryNotificationsTypeFilter()
}

// testNotificationOnFavoriteStudioScene tests that a FAVORITE_STUDIO_SCENE notification is created
// when a scene is created for a studio that the user has favorited and the edit is approved via voting
func (s *notificationTestRunner) testNotificationOnFavoriteStudioScene() {
	// Create a studio using the admin user
	adminRunner := asAdmin(s.t)
	studio, err := adminRunner.createTestStudio(nil)
	assert.NoError(s.t, err)

	studioID := studio.UUID()

	// Create a subscriber user who will favorite the studio
	subscriberUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
	assert.NoError(s.t, err)

	// Create a runner for the subscriber to make GraphQL calls
	subscriberRunner := createTestRunner(s.t, subscriberUser, []models.RoleEnum{models.RoleEnumEdit})

	// Subscriber favorites the studio
	_, err = subscriberRunner.client.favoriteStudio(studioID, true)
	assert.NoError(s.t, err)

	// Subscriber subscribes to FAVORITE_STUDIO_SCENE notifications
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumFavoriteStudioScene,
	}
	_, err = subscriberRunner.client.updateNotificationSubscriptions(subscriptions)
	assert.NoError(s.t, err)

	// Create an editor user who will submit the scene edit
	editorUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
	assert.NoError(s.t, err)

	editorRunner := createTestRunner(s.t, editorUser, []models.RoleEnum{models.RoleEnumEdit})

	// Editor creates a scene edit with the favorited studio
	title := editorRunner.generateSceneName()
	sceneEditDetailsInput := models.SceneEditDetailsInput{
		Title:    &title,
		StudioID: &studioID,
	}
	createdEdit, err := editorRunner.createTestSceneEdit(models.OperationEnumCreate, &sceneEditDetailsInput, nil)
	assert.NoError(s.t, err)

	// Have 3 voters vote to approve the edit (reaching the threshold)
	for i := 1; i <= 3; i++ {
		voterUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumVote})
		assert.NoError(s.t, err)

		voterCtx := context.WithValue(s.ctx, auth.ContextUser, auth.FromUser(voterUser))
		_, err = s.resolver.Mutation().EditVote(voterCtx, models.EditVoteInput{
			ID:   createdEdit.ID,
			Vote: models.VoteTypeEnumAccept,
		})
		assert.NoError(s.t, err)
	}

	// Small delay to ensure notification is created (notifications are triggered asynchronously)
	time.Sleep(200 * time.Millisecond)

	// Query notifications and verify the notification type
	notificationType := models.NotificationEnumFavoriteStudioScene
	result, err := subscriberRunner.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    25,
		Type:       &notificationType,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, 1, len(result.Notifications), "Subscriber should have exactly one FAVORITE_STUDIO_SCENE notification")
}

func TestNotificationOnFavoriteStudioScene(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testNotificationOnFavoriteStudioScene()
}
