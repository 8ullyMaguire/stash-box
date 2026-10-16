import type { FC } from "react";
import { EditList } from "src/components/list";
import Title from "src/components/title";
import {
  SortDirectionEnum,
  UserVotedFilterEnum,
  VoteStatusEnum,
} from "src/graphql";

const EditsComponent: FC = () => (
  <>
    <Title page="Edits" />
    <h3>Edits</h3>
    <EditList
      defaultVoteStatus={VoteStatusEnum.PENDING}
      defaultVoted={UserVotedFilterEnum.NOT_VOTED}
      defaultBot="exclude"
      defaultUserSubmitted={true}
      // Oldest first (SPEC 7.24.10). A queue is worked from the end that has been
      // waiting longest, and the sort control above still switches it: this is the
      // default the page OPENS on, not a restriction.
      defaultDirection={SortDirectionEnum.ASC}
    />
  </>
);

export default EditsComponent;
