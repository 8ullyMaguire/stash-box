import { useApolloClient } from "@apollo/client/react";
import debounce from "p-debounce";
import { type FC, type KeyboardEvent, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  components,
  type GroupBase,
  type OnChangeValue,
  type SelectInstance,
} from "react-select";
import Async from "react-select/async";
import {
  GenderIcon,
  SearchHint,
  SearchInput,
  Thumbnail,
} from "src/components/fragments";
import type { SearchAllQuery, SearchPerformersQuery } from "src/graphql";
import SearchAllGQL from "src/graphql/queries/SearchAll.gql";
import SearchPerformersGQL from "src/graphql/queries/SearchPerformers.gql";
import {
  handleResult,
  type PerformerResult,
  type SceneResult,
  type SearchResult,
} from "./handleResult";

export type { PerformerResult, SceneResult };

export enum SearchType {
  Performer = "performer",
  Combined = "combined",
}

/**
 * The id this field's input gets when the caller supplies none.
 *
 * react-select numbers its ids itself (`react-select-2-input`) when `inputId` is absent, and an input with a
 * generated id has no label to point at it: axe reports `label` [critical] -- "Form elements must have
 * labels". Callers that pass `inputId` are the ones rendering their own `<label htmlFor>` (that is the
 * convention `e2e/support/helpers/forms.ts` documents); a caller that does not now gets a real label and a
 * stable id rather than a numbered one.
 */
const DEFAULT_SEARCH_INPUT_ID = "search-field-input";

interface SearchFieldProps {
  onClick?: (result: SceneResult | PerformerResult) => void;
  onClickPerformer?: (result: PerformerResult) => void;
  searchType: SearchType;
  excludeIDs?: string[];
  nav?: boolean;
  placeholder?: string;
  showAllLink?: boolean;
  autoFocus?: boolean;
  /** When provided, performers who have performed for this studio's network will be sorted to the top */
  studioId?: string;
  inputId?: string;
}

const ValueContainer: typeof components.ValueContainer = (props) => (
  <>
    <SearchHint />
    <components.ValueContainer {...props} />
  </>
);

const DropdownIndicator = () => null;
const IndicatorSeparator = () => null;

const valueIsPerformer = (
  arg?: SceneResult | PerformerResult,
): arg is PerformerResult => arg?.__typename === "Performer";

const formatOptionLabel = ({ label, sublabel, value }: SearchResult) => (
  <div className="d-flex">
    {valueIsPerformer(value) && (
      <Thumbnail
        image={value.thumbnail?.url}
        className="SearchField-thumb"
        alt={value.name}
        size={300}
        orientation="portrait"
      />
    )}
    <div>
      <div className="search-value">
        {valueIsPerformer(value) && <GenderIcon gender={value.gender} />}
        {value?.deleted ? <del>{label}</del> : label}
      </div>
      <div className="search-subvalue">{sublabel}</div>
    </div>
  </div>
);

const SearchField: FC<SearchFieldProps> = ({
  onClick,
  onClickPerformer,
  searchType = SearchType.Performer,
  excludeIDs = [],
  nav = false,
  placeholder,
  showAllLink = false,
  autoFocus = false,
  studioId,
  inputId,
}) => {
  const client = useApolloClient();
  const navigate = useNavigate();
  const [selectedValue, setSelected] = useState(null);
  const searchTerm = useRef("");
  const selectRef =
    useRef<SelectInstance<SearchResult, false, GroupBase<SearchResult>>>(null);

  const handleSearch = async (term: string) => {
    if (term) {
      const { data } = await client.query<
        SearchPerformersQuery | SearchAllQuery
      >({
        query:
          searchType === SearchType.Performer
            ? SearchPerformersGQL
            : SearchAllGQL,
        variables: {
          term,
          ...(searchType === SearchType.Performer && studioId
            ? { studioId, hasStudioId: true }
            : {}),
        },
        fetchPolicy: "network-only",
      });
      if (!data) return [];
      return handleResult(data, excludeIDs, showAllLink, studioId);
    }
    return [];
  };

  const debouncedLoadOptions = debounce(handleSearch, 400);

  const handleLoad = (term: string) => {
    searchTerm.current = term;
    return debouncedLoadOptions(term);
  };

  const handleChange = (result: OnChangeValue<SearchResult, false>) => {
    if (result?.type === "ALL")
      return navigate(`/search?q=${encodeURIComponent(searchTerm.current)}`);

    if (result?.value) {
      if (valueIsPerformer(result.value)) onClickPerformer?.(result.value);
      onClick?.(result.value);
      if (nav) navigate(`/${result.type}s/${result.value.id}`);
    }

    setSelected(null);
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    if (e.key === "Enter" && searchTerm.current && showAllLink) {
      navigate(`/search?q=${encodeURIComponent(searchTerm.current)}`);
      selectRef?.current?.blur();
    }
  };

  return (
    <div className="SearchField">
      {!inputId && (
        <label htmlFor={DEFAULT_SEARCH_INPUT_ID} className="visually-hidden">
          Search
        </label>
      )}
      <Async
        autoFocus={autoFocus}
        inputId={inputId ?? DEFAULT_SEARCH_INPUT_ID}
        classNamePrefix="react-select"
        value={selectedValue}
        loadOptions={handleLoad}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        ref={selectRef}
        placeholder={
          placeholder ??
          (searchType === SearchType.Performer
            ? "Search for performer..."
            : "Search for performer or scene...")
        }
        formatOptionLabel={formatOptionLabel}
        components={{
          DropdownIndicator,
          IndicatorSeparator,
          ValueContainer,
          Input: SearchInput,
        }}
        noOptionsMessage={({ inputValue }) =>
          inputValue === "" ? null : `No result found for "${inputValue}"`
        }
      />
    </div>
  );
};

export default SearchField;
