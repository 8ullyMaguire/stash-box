import { useLazyQuery, useQuery } from "@apollo/client/react";
import {
  CategoriesDocument,
  CategoryDocument,
  type CategoryQueryVariables,
  ConfigDocument,
  CurationDashboardDocument,
  DraftDocument,
  type DraftQueryVariables,
  DraftsDocument,
  EditDocument,
  type EditQueryVariables,
  EditsDocument,
  type EditsQueryVariables,
  EditUpdateDocument,
  type EloEntityType,
  EloLeaderboardDocument,
  EloMatchupDocument,
  FetchSiteFaviconsDocument,
  type FetchSiteFaviconsQuery,
  type FetchSiteFaviconsQueryVariables,
  FingerprintClustersDocument,
  type FingerprintClustersQueryVariables,
  FullPerformerDocument,
  ImageTypeGroupsDocument,
  type ImageTypeGroupsQueryVariables,
  MeDocument,
  type MeQuery,
  type MeQueryVariables,
  ModAuditsDocument,
  type ModAuditsQueryVariables,
  NotificationsDocument,
  type NotificationsQueryVariables,
  PairingScenesDocument,
  type PairingScenesQueryVariables,
  PendingEditsCountDocument,
  type PendingEditsCountQueryVariables,
  PerformerDocument,
  type PerformerQueryVariables,
  PerformersDocument,
  type PerformersQueryVariables,
  PublicUserDocument,
  type PublicUserQueryVariables,
  QueryExistingPerformerDocument,
  type QueryExistingPerformerQueryVariables,
  QueryExistingSceneDocument,
  type QueryExistingSceneQueryVariables,
  SceneCountDocument,
  type SceneCountQueryVariables,
  SceneDocument,
  ScenePairingsDocument,
  type ScenePairingsQueryVariables,
  type SceneQueryVariables,
  ScenesDocument,
  type ScenesQueryVariables,
  ScenesWithFingerprintsDocument,
  type ScenesWithFingerprintsQueryVariables,
  SearchAllDocument,
  type SearchAllQuery,
  type SearchAllQueryVariables,
  SearchPerformersDocument,
  type SearchPerformersQuery,
  type SearchPerformersQueryVariables,
  SearchScenesDocument,
  type SearchScenesQuery,
  type SearchScenesQueryVariables,
  SearchTagsDocument,
  type SearchTagsQueryVariables,
  SiteCategoriesDocument,
  SiteCategoryDocument,
  type SiteCategoryQueryVariables,
  SiteDocument,
  type SiteQueryVariables,
  SitesDocument,
  StudioDocument,
  StudioPerformerScenesDocument,
  type StudioPerformerScenesQueryVariables,
  StudioPerformersDocument,
  type StudioPerformersQueryVariables,
  type StudioQueryVariables,
  StudiosDocument,
  type StudiosQuery,
  type StudiosQueryVariables,
  SubStudiosDocument,
  type SubStudiosQueryVariables,
  TagDocument,
  type TagQueryVariables,
  TagsDocument,
  type TagsQuery,
  type TagsQueryVariables,
  UnorganizedImagesDocument,
  type UnorganizedImagesQueryVariables,
  UnreadNotificationCountDocument,
  UserDocument,
  type UserQueryVariables,
  UsersDocument,
  type UsersQueryVariables,
  VersionDocument,
} from "../types";

export const useCategory = (variables: CategoryQueryVariables, skip = false) =>
  useQuery(CategoryDocument, {
    variables,
    skip,
  });

export const useCategories = () => useQuery(CategoriesDocument);

export const useImageTypeGroups = (variables: ImageTypeGroupsQueryVariables) =>
  useQuery(ImageTypeGroupsDocument, { variables });

export const useEdit = (variables: EditQueryVariables, skip = false) =>
  useQuery(EditDocument, {
    variables,
    skip,
  });

export const useEditUpdate = (variables: EditQueryVariables, skip = false) =>
  useQuery(EditUpdateDocument, {
    variables,
    skip,
  });

export const useEdits = (variables: EditsQueryVariables) =>
  useQuery(EditsDocument, {
    variables,
  });

export const useMe = (options?: useQuery.Options<MeQuery, MeQueryVariables>) =>
  useQuery(MeDocument, options);

export const usePerformer = (
  variables: PerformerQueryVariables,
  skip = false,
) =>
  useQuery(PerformerDocument, {
    variables,
    skip,
  });

export const useFullPerformer = (
  variables: PerformerQueryVariables,
  skip = false,
) =>
  useQuery(FullPerformerDocument, {
    variables,
    skip,
  });

export const usePerformers = (variables: PerformersQueryVariables) =>
  useQuery(PerformersDocument, {
    variables,
  });

export const useScene = (variables: SceneQueryVariables, skip = false) =>
  useQuery(SceneDocument, {
    variables,
    skip,
  });

export const useScenes = (variables: ScenesQueryVariables, skip = false) =>
  useQuery(ScenesDocument, {
    variables,
    skip,
  });

export const useScenesWithFingerprints = (
  variables: ScenesWithFingerprintsQueryVariables,
  skip = false,
) =>
  useQuery(ScenesWithFingerprintsDocument, {
    variables,
    skip,
  });

export const useSceneCount = (
  variables: SceneCountQueryVariables,
  skip = false,
) =>
  useQuery(SceneCountDocument, {
    variables,
    skip,
  });

export const useSearchAll = (
  variables: SearchAllQueryVariables,
  skip = false,
) =>
  useQuery(SearchAllDocument, {
    variables,
    skip,
  });

export const useSearchPerformers = (
  variables: SearchPerformersQueryVariables,
  skip = false,
) =>
  useQuery(SearchPerformersDocument, {
    variables,
    skip,
  });

export const useSearchScenes = (
  variables: SearchScenesQueryVariables,
  skip = false,
) =>
  useQuery(SearchScenesDocument, {
    variables,
    skip,
  });

export const useLazySearchAll = (
  options?: useLazyQuery.Options<SearchAllQuery, SearchAllQueryVariables>,
) => useLazyQuery(SearchAllDocument, options);

export const useLazySearchPerformers = (
  options?: useLazyQuery.Options<
    SearchPerformersQuery,
    SearchPerformersQueryVariables
  >,
) => useLazyQuery(SearchPerformersDocument, options);

export const useLazySearchScenes = (
  options?: useLazyQuery.Options<SearchScenesQuery, SearchScenesQueryVariables>,
) => useLazyQuery(SearchScenesDocument, options);

export const useSearchTags = (variables: SearchTagsQueryVariables) =>
  useQuery(SearchTagsDocument, {
    variables,
  });

export const useStudio = (variables: StudioQueryVariables, skip = false) =>
  useQuery(StudioDocument, {
    variables,
    skip,
  });

export const useStudios = (variables: StudiosQueryVariables) =>
  useQuery(StudiosDocument, {
    variables,
  });

export const useSubStudios = (variables: SubStudiosQueryVariables) =>
  useQuery(SubStudiosDocument, {
    variables,
  });

export const useLazyStudios = (
  options?: useLazyQuery.Options<StudiosQuery, StudiosQueryVariables>,
) => useLazyQuery(StudiosDocument, options);

export const useTag = (variables: TagQueryVariables, skip = false) =>
  useQuery(TagDocument, {
    variables,
    skip,
  });

export const useTags = (variables: TagsQueryVariables) =>
  useQuery(TagsDocument, {
    variables,
  });
export const useLazyTags = (
  options?: useLazyQuery.Options<TagsQuery, TagsQueryVariables>,
) => useLazyQuery(TagsDocument, options);

export const usePrivateUser = (variables: UserQueryVariables, skip = false) =>
  useQuery(UserDocument, {
    variables,
    skip,
  });
export const usePublicUser = (
  variables: PublicUserQueryVariables,
  skip = false,
) =>
  useQuery(PublicUserDocument, {
    variables,
    skip,
  });

export const useUsers = (variables: UsersQueryVariables) =>
  useQuery(UsersDocument, {
    variables,
  });

export const useConfig = () => useQuery(ConfigDocument);

export const useFingerprintClusters = (
  variables: FingerprintClustersQueryVariables,
  skip = false,
) =>
  useQuery(FingerprintClustersDocument, {
    variables,
    skip,
    fetchPolicy: "no-cache",
  });

export const useVersion = () => useQuery(VersionDocument);

export const usePendingEditsCount = (
  variables: PendingEditsCountQueryVariables,
) => useQuery(PendingEditsCountDocument, { variables });

export const useSite = (variables: SiteQueryVariables, skip = false) =>
  useQuery(SiteDocument, {
    variables,
    skip,
  });

export const useSites = () => useQuery(SitesDocument);

export const useSiteCategory = (
  variables: SiteCategoryQueryVariables,
  skip = false,
) =>
  useQuery(SiteCategoryDocument, {
    variables,
    skip,
  });

export const useSiteCategories = () => useQuery(SiteCategoriesDocument);

export const useLazyFetchSiteFavicons = (
  options?: useLazyQuery.Options<
    FetchSiteFaviconsQuery,
    FetchSiteFaviconsQueryVariables
  >,
) => useLazyQuery(FetchSiteFaviconsDocument, options);

export const useDraft = (variables: DraftQueryVariables, skip = false) =>
  useQuery(DraftDocument, {
    variables,
    skip,
  });

export const useDrafts = () => useQuery(DraftsDocument);

export const useQueryExistingScene = (
  variables: QueryExistingSceneQueryVariables,
  skip = false,
) =>
  useQuery(QueryExistingSceneDocument, {
    variables,
    skip,
  });

export const useQueryExistingPerformer = (
  variables: QueryExistingPerformerQueryVariables,
  skip = false,
) =>
  useQuery(QueryExistingPerformerDocument, {
    variables,
    skip,
  });

export const useScenePairings = (variables: ScenePairingsQueryVariables) =>
  useQuery(ScenePairingsDocument, {
    variables,
  });

export const usePairingScenes = (
  variables: PairingScenesQueryVariables,
  skip = false,
) =>
  useQuery(PairingScenesDocument, {
    variables,
    skip,
  });

export const useStudioPerformers = (
  variables: StudioPerformersQueryVariables,
) =>
  useQuery(StudioPerformersDocument, {
    variables,
  });

export const useStudioPerformerScenes = (
  variables: StudioPerformerScenesQueryVariables,
  skip = false,
) =>
  useQuery(StudioPerformerScenesDocument, {
    variables,
    skip,
  });

export const useNotifications = (variables: NotificationsQueryVariables) =>
  useQuery(NotificationsDocument, {
    variables,
  });

export const useUnreadNotificationsCount = (skip = false) =>
  useQuery(UnreadNotificationCountDocument, { skip });

export const useModAudits = (variables: ModAuditsQueryVariables) =>
  useQuery(ModAuditsDocument, {
    variables,
  });

export const useUnorganizedImages = (
  variables: UnorganizedImagesQueryVariables,
) =>
  useQuery(UnorganizedImagesDocument, {
    variables,
  });

// Curation: the gamification surface.
//
// None of this existed in the client before. The backend has had Elo, trust,
// quests and completion scores exposed for a while -- 38 GraphQL fields -- and
// nothing in the UI called any of it, so none of it was reachable by a user
// without hand-writing a query.
//
// DEFAULT_COMPLETION_THRESHOLD is 40 because it is the number the completion
// score is built around: below it a record is still missing enough to be worth
// someone else's attention. It is a named constant rather than a literal at the
// call site because the same threshold has to mean the same thing on the
// dashboard, on a record's own page, and in any copy describing it.
export const DEFAULT_COMPLETION_THRESHOLD = 40;

export const useCurationDashboard = (
  threshold = DEFAULT_COMPLETION_THRESHOLD,
) => useQuery(CurationDashboardDocument, { variables: { threshold } });

export const useEloMatchup = (entityType: EloEntityType, skip = false) =>
  useQuery(EloMatchupDocument, {
    variables: { entityType },
    skip: skip || !entityType,
  });

export const useEloLeaderboard = (entityType: EloEntityType, limit?: number) =>
  useQuery(EloLeaderboardDocument, {
    variables: { entityType, limit },
    skip: !entityType,
  });
