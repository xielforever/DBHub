import type { DashboardReview } from '../../api/managed-dashboard'

type ReviewAccess = Pick<DashboardReview, 'status' | 'reviewer_eligible' | 'requires_re_review'
  | 'submitted_by' | 'business_signer_id' | 'can_publish'>

export function needsDashboardReviewAgain(review?: ReviewAccess) {
  return review?.status === 'approved' &&
    (review.requires_re_review ?? (review.reviewer_eligible === false))
}

export function canPublishDashboardReview(review: ReviewAccess | undefined, actorId: number | undefined, roleAllows: boolean) {
  if (!roleAllows || review?.status !== 'approved' || review.reviewer_eligible === false ||
    needsDashboardReviewAgain(review)) return false
  if (review.can_publish === undefined) return true
  return review.can_publish && actorId !== undefined && actorId === review.business_signer_id &&
    actorId !== review.submitted_by
}
