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

// pollUntil waits for cond to hold, or the notification poll timeout to expire.
// It returns whether the condition was met and never fails the test itself, so
// the caller can report what it actually saw -- which is the whole point when the
// symptom is "a row was not there yet".
func (s *notificationTestRunner) pollUntil(cond func() bool) bool {
	deadline := time.Now().Add(notificationPollTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// awaitUnreadAbove waits until the unread total exceeds n, or the poll timeout
// expires. It is the plain "wait for my notifications" case, for tests that
// assert something about arrival rather than about a filtered count.
func (s *notificationTestRunner) awaitUnreadAbove(n int) {
	s.pollUntil(func() bool {
		count, err := s.client.getUnreadNotificationCount()
		return err == nil && count.Total > n
	})
}

// awaitQuiet waits for the unread count to stop changing, for tests whose next
// step asserts that something did NOT happen.
//
// Polling for a rise cannot express "wait for this to have run", because the row
// it would wait for is deliberately absent -- that absence is what the following
// assertion is about. So this waits for stability instead: the count holds steady
// across two consecutive samples.
func (s *notificationTestRunner) awaitQuiet() {
	previous := -1
	stable := 0
	deadline := time.Now().Add(notificationPollTimeout)
	for time.Now().Before(deadline) {
		count, err := s.client.getUnreadNotificationCount()
		if err == nil {
			if count.Total == previous {
				stable++
				if stable >= 2 {
					return
				}
			} else {
				stable = 0
				previous = count.Total
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.t.Log("awaitQuiet: the unread count never held steady before the deadline")
}

// drainNotifications marks every unread notification as read and WAITS for the
// count to actually reach zero, rather than sleeping and hoping.
//
// The wait is the whole point. A bare `markNotificationsRead` followed by a fixed
// sleep is what made testQueryNotificationsTypeFilter order-dependent: the mark
// is a database write, and a notification goroutine from another test can commit
// after it and before the sleep expires. Polling for zero and only then sampling
// the baseline is what makes a delta meaningful.
//
// It returns the count it observed, so a caller that wants a baseline gets one
// that was verified rather than assumed.
func (s *notificationTestRunner) drainNotifications() {
	if _, err := s.client.markNotificationsRead(nil); err != nil {
		s.t.Fatalf("draining notifications: %v", err)
	}

	deadline := time.Now().Add(notificationPollTimeout)
	for time.Now().Before(deadline) {
		count, err := s.client.getUnreadNotificationCount()
		if err == nil && count.Total == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Not a hard failure: a stubborn row should not mask the assertions that
	// follow, and those now measure from whatever the baseline turned out to be.
	// Logging rather than failing is what keeps a leaked row visible in the
	// output instead of only in an arithmetic difference.
	count, err := s.client.getUnreadNotificationCount()
	if err == nil {
		s.t.Logf("drainNotifications: %d unread remained after the deadline; "+
			"the baseline below will include them", count.Total)
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

	// Wait for the count to settle, rather than sleeping 100ms and hoping.
	//
	// This is the hardest shape of the pattern in this file: the assertion is
	// that NOTHING happens, so there is no rising count to poll for and a fixed
	// sleep is the only thing standing between a slow goroutine and a PASS that
	// means nothing. awaitQuiet waits for the count to hold steady across two
	// samples, which is the strongest statement available -- and it is still not
	// a proof, so the comment says so rather than implying the test is airtight.
	s.awaitQuiet()

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
	// First, clear all existing notifications by marking them all as read --
	// and WAIT for the count to reach zero rather than sleeping past it, so the
	// baseline sampled below is one that was observed rather than assumed.
	s.drainNotifications()

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

	// Wait for the notification to actually be created, polled. A longer fixed
	// sleep is still a guess at a latency, and a guess that is wrong in the slow
	// direction is a false pass.
	s.awaitUnreadAbove(0)

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

	// Wait for the count to fall, polled. Marking as read is a write followed by
	// a read of a count, and sleeping between them is how a read ends up
	// observing the state before its own write landed.
	s.pollUntil(func() bool {
		count, err := s.client.getUnreadNotificationCount()
		return err == nil && count.Total < unreadCountBefore.Total
	})

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

	// Wait for this test's own notifications to arrive. The absolute count below
	// is safe ONLY because of that: the baseline is sampled after a verified
	// drain, so a row leaking in from another test shows as a delta with the
	// baseline printed beside it, rather than as a mysterious total.
	unreadBefore, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)
	s.awaitUnreadAbove(unreadBefore.Total)

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
	// Polled for the real condition. "The notification is raised in a goroutine"
	// is the reason this cannot be a sleep, and the fix is the same shape as the
	// tests above: observe, do not guess a latency.
	if !s.pollUntil(func() bool { return countDownvotes() >= baseline+1 }) {
		s.t.Fatalf("the reject-vote notification never appeared (%d, want %d)",
			countDownvotes(), baseline+1)
	}
	assert.Equal(s.t, baseline+1, countDownvotes(),
		"a reject vote should raise a DOWNVOTE_OWN_EDIT notification")

	// 2. The same voter changes their vote to accept.
	_, err = s.resolver.Mutation().EditVote(voterCtx, models.EditVoteInput{
		ID:   createdEdit.ID,
		Vote: models.VoteTypeEnumAccept,
	})
	assert.NoError(s.t, err)

	// 3. The notification must be gone — the edit is no longer downvoted.
	//
	// Polled for the FALL, and this is the direction that is easy to get wrong:
	// a fixed sleep after a deletion passes almost always and fails exactly when
	// the database is slow, which is when it matters. The comment here used to
	// describe that poll and then not make one -- the sleep had been removed and
	// the assertion left to run immediately, which fails as "expected 0, actual
	// 1". A comment about waiting is not a wait.
	if !s.pollUntil(func() bool { return countDownvotes() == baseline }) {
		s.t.Fatalf("the notification was not cleared: %d downvote row(s) remain, "+
			"want baseline(%d)", countDownvotes(), baseline)
	}
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
	// Polled, not slept on. Already delta-based and correctly reasoned, but it
	// still ended on a fixed sleep, and these rows come from the same bare `go`
	// that made its siblings flaky. It failed under `make it` with "expected 3,
	// actual 2" -- a MISSING row, not a leaked one, so invisible to any check
	// that only guards against extras.
	if !s.pollUntil(func() bool { return countDownvotes() >= baseline+2 }) {
		s.t.Fatalf("only %d downvote notification(s) appeared, want baseline(%d)+2",
			countDownvotes(), baseline)
	}
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

	// The count must NOT drop, so "wait until it rises" is the wrong condition
	// here -- this step deliberately changes nothing upward. awaitQuiet waits for
	// the count to hold steady instead, which is a weaker guarantee than a rise
	// and is stated as such: the assertion below is about the count not dropping,
	// and no count alone can distinguish "not arrived yet" from "dropped".
	s.awaitQuiet()

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
	// Same fix, same reason as testQueryNotificationsTypeFilter, and the two
	// failed together in the same run: a mark-as-read, a fixed sleep, and then
	// absolute counts. A notification goroutine from an earlier test commits
	// after the mark and the total comes back as 6 rather than 5.
	//
	// Pagination is the sharper case, because the assertions that matter here
	// are the PAGE SIZES and those stay correct however many extra rows arrive --
	// the leaked row only shows up in the COUNT. So the count is asserted against
	// a verified baseline, and the per-page assertions below are left alone: a
	// leaked row is not this test's business to page through, and inflating
	// perPage to accommodate one would destroy what the test is for.
	s.drainNotifications()
	baseline, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)

	// Subscribe to comment notifications
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumCommentOwnEdit,
	}
	_, err = s.client.updateNotificationSubscriptions(subscriptions)
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

	// Wait for all FIVE of this test's own notifications, polled rather than
	// slept on. The helper requires both Total and Urgent to rise, which is what
	// stops a lingering goroutine from satisfying the wait on its own.
	if _, err := s.awaitUnreadCountsAbove(models.UnreadNotificationCount{
		Total: baseline.Total + 5,
	}); err != nil {
		s.t.Fatalf("the five notifications never arrived: %v", err)
	}

	// Test pagination with perPage=2
	perPage := 2

	// Fetch page 1
	page1Result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    perPage,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, int(baseline.Total)+5, page1Result.Count,
		"total should be baseline(%d) + 5", baseline.Total)
	assert.Equal(s.t, 2, len(page1Result.Notifications), "Page 1 should have 2 notifications")

	// Fetch page 2
	page2Result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       2,
		PerPage:    perPage,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, int(baseline.Total)+5, page2Result.Count,
		"total should still be baseline(%d) + 5", baseline.Total)
	assert.Equal(s.t, 2, len(page2Result.Notifications), "Page 2 should have 2 notifications")

	// Fetch page 3
	page3Result, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       3,
		PerPage:    perPage,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)
	assert.Equal(s.t, int(baseline.Total)+5, page3Result.Count,
		"total should still be baseline(%d) + 5", baseline.Total)
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
	assert.Equal(s.t, int(baseline.Total)+5, page4Result.Count,
		"total should still be baseline(%d) + 5", baseline.Total)
	assert.Equal(s.t, 0, len(page4Result.Notifications), "Page 4 should have 0 notifications")
}

func TestQueryNotificationsPagination(t *testing.T) {
	pt := createNotificationTestRunner(t)
	pt.testQueryNotificationsPagination()
}

// testQueryNotificationsTypeFilter tests that the type filter works correctly
func (s *notificationTestRunner) testQueryNotificationsTypeFilter() {
	// Establish a CLEAN SLATE that is verified rather than assumed, and then
	// assert RELATIVE to it.
	//
	// This test used to mark everything read, sleep 100ms, and then assert
	// hard-coded absolute counts of 2 and 1. That is order-dependent: the edit
	// mutations fire their notifications from a bare `go`, so a goroutine
	// belonging to an EARLIER test can commit a row inside the 100ms window,
	// after the mark-as-read. It then stays unread and inflates the total, and
	// the failure reads "expected 2, actual 3" with no hint that the third row
	// was never this test's.
	//
	// It reproduced under `make it` in 434s of package run, and passed in
	// isolation every time -- which is the signature of a race, not a defect in
	// the filter being tested.
	//
	// The fix is the same one this file already uses in awaitUnreadCountsAbove:
	// poll for the real condition instead of sleeping a fixed interval, and
	// measure from a baseline rather than from zero. Note that DRAINING FIRST is
	// not sufficient on its own -- a row can still arrive after the drain -- so
	// the counts below are deltas, which is what makes an unexpected extra row
	// visible as a delta of 2 rather than as a mysterious total of 3.
	s.drainNotifications()
	baseline, err := s.client.getUnreadNotificationCount()
	assert.NoError(s.t, err)

	// Subscribe to multiple notification types
	subscriptions := []models.NotificationEnum{
		models.NotificationEnumCommentOwnEdit,
		models.NotificationEnumDownvoteOwnEdit,
	}
	_, err = s.client.updateNotificationSubscriptions(subscriptions)
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

	// Wait for BOTH of this test's own notifications to arrive, rather than
	// sleeping and hoping. The deadline is the existing helper's.
	if _, err := s.awaitUnreadCountsAbove(models.UnreadNotificationCount{
		Total: baseline.Total + 2,
	}); err != nil {
		s.t.Fatalf("both notifications never arrived: %v", err)
	}

	// Query all notifications (no type filter)
	allResult, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    25,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)

	// The expected total is the baseline PLUS this test's two, not the constant
	// 2. A row that leaked in from another test is then a delta of 1 rather than
	// an unexplained total, and a row that is genuinely extra shows up as a
	// delta of 3 with the baseline printed next to it.
	wantTotal := int(baseline.Total) + 2
	assert.Equal(s.t, wantTotal, allResult.Count,
		"unread notifications should be baseline(%d) + 2", baseline.Total)
	assert.Equal(s.t, wantTotal, len(allResult.Notifications),
		"should return as many notifications as it counted")

	// Query only COMMENT_OWN_EDIT notifications
	commentNotificationType := models.NotificationEnumCommentOwnEdit
	commentResult, err := s.client.queryNotifications(models.QueryNotificationsInput{
		Page:       1,
		PerPage:    25,
		Type:       &commentNotificationType,
		UnreadOnly: new(true),
	})
	assert.NoError(s.t, err)

	// FILTERED counts stay absolute, and that is now correct rather than lucky:
	// the two notification types this test subscribes to are created by this
	// test alone, so a leaked row from elsewhere cannot be one of them. The
	// TOTAL was the order-dependent assertion; these never were, and turning
	// them into deltas would hide a genuine duplicate COMMENT_OWN_EDIT row.
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

	// Verify the sum of filtered notifications equals the total, MINUS the
	// baseline. Before this was `commentResult.Count + downvoteResult.Count ==
	// allResult.Count`, which held only while the total happened to be exactly
	// this test's two notifications. With a baseline it states the real
	// invariant: this test's own notifications are exactly the two it created,
	// and nothing else of its own is unaccounted for.
	totalFiltered := commentResult.Count + downvoteResult.Count
	assert.Equal(s.t, int(baseline.Total)+totalFiltered, allResult.Count,
		"baseline(%d) + the two filtered counts should equal the total", baseline.Total)
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

	// Polled rather than delayed: the notification is raised asynchronously, so
	// the condition to wait for is its arrival, not a guessed latency.
	//
	// Polled on the SUBSCRIBER's notifications, which is who receives this one --
	// a first attempt waited on the main test user's unread count instead, which
	// is a different account entirely. It passed in isolation and failed in the
	// group, because a neighbouring test's notification could satisfy the wrong
	// account's count and release the wait while the subscriber's row was still
	// in flight. Waiting on the thing being asserted is not a detail: it is the
	// difference between a wait and a coincidence.
	notificationType := models.NotificationEnumFavoriteStudioScene
	countSubscriberNotifications := func() int {
		res, err := subscriberRunner.client.queryNotifications(models.QueryNotificationsInput{
			Page:       1,
			PerPage:    25,
			Type:       &notificationType,
			UnreadOnly: new(true),
		})
		if err != nil {
			return 0
		}
		return len(res.Notifications)
	}

	if !s.pollUntil(func() bool { return countSubscriberNotifications() >= 1 }) {
		s.t.Fatalf("the subscriber never received a FAVORITE_STUDIO_SCENE notification")
	}

	// Query notifications and verify the notification type
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
