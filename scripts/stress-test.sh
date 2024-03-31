#!/bin/bash

# set -x

if [[ $# -ne 5 ]]; then
    echo "Wrong number of args"
    echo "Usage:"
    echo "./stress-test.sh username_prefix num_users parrallel_builds ludus_url action"
    echo ""
    echo "You must set an admin API key in ADMIN_API_KEY before running this script"
    echo "You must have an SSH tunnel to the Ludus host with '-L 8081:127.0.0.1:8081' open before running this script"
    echo "action must be 'run' or 'destroy'"
    echo ""
    exit 1
fi

if ! command -v ludus &> /dev/null
then
    echo "ludus command not in PATH"
    exit 1
fi

if [[ -z ${ADMIN_API_KEY} ]]; then
    echo "You must set the ADMIN_API_KEY env var to create users"
    exit 1
fi

USER_PREFIX=$1
NUMBER_OF_USERS=$2
NUMBER_OF_CONCURRENT_DEPLOYS=$3
URL_FOR_LUDUS_TESTS=$4
ACTION=$5

# Array to keep track of running tasks
RUNNING_TASKS=()

# Function to check the status of a task
check_status() {
    check_status_user_id=$1
    status=$(ludus range list --url ${URL_FOR_LUDUS_TESTS} --user ${USER_PREFIX}${check_status_user_id} --json | jq -r '.rangeState')
    echo "User ID: ${USER_PREFIX}${check_status_user_id}, Status: $status"
}

# It was at this point, I relaized doing this in bash was a mistake
# bash builtin syntx for removing items from an array does not change the array length, just nulls the value. Neat.
remove_element_from_task_array() {
    element_to_remove=$1
    for (( i=0; i<${#RUNNING_TASKS[@]}; i++ )); do 
        if [[ ${RUNNING_TASKS[i]} == $element_to_remove ]]; then
            RUNNING_TASKS=( "${RUNNING_TASKS[@]:0:$i}" "${RUNNING_TASKS[@]:$((i + 1))}" )
            i=$((i - 1))
        fi
    done
}

if [[ $ACTION == "run" ]]; then

    echo "This script will create $NUMBER_OF_USERS new users with prefix $USER_PREFIX on the Ludus host and deploy the stress test range for them, $NUMBER_OF_CONCURRENT_DEPLOYS at a time on $URL_FOR_LUDUS_TESTS"
    echo "If you wish to bail, Ctrl+C now..."
    sleep 3
    echo "Creating users..."

    for i in $(seq ${NUMBER_OF_USERS}); do
        if ! LUDUS_API_KEY=${ADMIN_API_KEY} ludus --url ${URL_FOR_LUDUS_TESTS} users list all | grep -q "${USER_PREFIX}${i}"; then
            echo "${USER_PREFIX}${i} not present on Ludus, creating now"
            LUDUS_API_KEY=${ADMIN_API_KEY} ludus --url https://127.0.0.1:8081 user add -i "${USER_PREFIX}${i}" -n "$1 $i"
        fi
    done

    echo "Deploying ranges..."

    # Loop through user IDs from 1 to 30
    for user_id in $(seq ${NUMBER_OF_USERS}); do
        # Wait for a running task to finish if there are already 3 running
        while [ ${#RUNNING_TASKS[@]} -ge ${NUMBER_OF_CONCURRENT_DEPLOYS} ]; do
            for task in "${RUNNING_TASKS[@]}"; do
                check_status $task
                if [[ "$status" == "SUCCESS" || "$status" == "ERROR" ]]; then
                    remove_element_from_task_array $task
                fi
            done
            sleep 5
        done

        # Start a new task
        LUDUS_API_KEY=${ADMIN_API_KEY} ludus range config set -f stress-test-config.yml --url ${URL_FOR_LUDUS_TESTS} --user ${USER_PREFIX}${user_id}
        LUDUS_API_KEY=${ADMIN_API_KEY} ludus range deploy --url ${URL_FOR_LUDUS_TESTS} --user ${USER_PREFIX}${user_id}
        RUNNING_TASKS+=($user_id)
        echo "Started deploy task for User ID: ${USER_PREFIX}${user_id}"
    done

    # Wait for remaining tasks to finish
    while [ ${#RUNNING_TASKS[@]} -gt 0 ]; do
        for task in "${RUNNING_TASKS[@]}"; do
            check_status $task
            if [[ "$status" == "SUCCESS" || "$status" == "ERROR" ]]; then
                remove_element_from_task_array $task
            fi
        done
        sleep 5
    done

elif [[ $ACTION == "destroy" ]]; then

    echo "This script will destroy the ranges of $NUMBER_OF_USERS new users with prefix $USER_PREFIX and remove the user accounts, $NUMBER_OF_CONCURRENT_DEPLOYS at a time on $URL_FOR_LUDUS_TESTS"
    echo "If you wish to bail, Ctrl+C now..."
    sleep 3
    echo "Destroying ranges..."

    # Loop through user IDs from 1 to 30
    for user_id in $(seq ${NUMBER_OF_USERS}); do
        # Wait for a running task to finish if there are already 3 running
        while [ ${#RUNNING_TASKS[@]} -ge ${NUMBER_OF_CONCURRENT_DEPLOYS} ]; do
            for task in "${RUNNING_TASKS[@]}"; do
                check_status $task
                if [[ "$status" == "DESTROYED" || "$status" == "ERROR" || "$status" == "" ]]; then
                    remove_element_from_task_array $task
                fi
            done
            sleep 5
        done

        # Start a new task
        LUDUS_API_KEY=${ADMIN_API_KEY} ludus range rm --no-prompt --url ${URL_FOR_LUDUS_TESTS} --user ${USER_PREFIX}${user_id}
        RUNNING_TASKS+=($user_id)
        echo "Started rm task for User ID: ${USER_PREFIX}${user_id}"
    done
    # Wait for remaining tasks to finish
    while [ ${#RUNNING_TASKS[@]} -gt 0 ]; do
        for task in "${RUNNING_TASKS[@]}"; do
            check_status $task
            if [[ "$status" == "DESTROYED" || "$status" == "ERROR" || "$status" == "" ]]; then
                remove_element_from_task_array $task
            fi
        done
        sleep 5
    done

    echo "Removing users..."

    for i in $(seq ${NUMBER_OF_USERS}); do
        if LUDUS_API_KEY=${ADMIN_API_KEY} ludus --url ${URL_FOR_LUDUS_TESTS} users list all | grep -q "${USER_PREFIX}${i}"; then
            echo "${USER_PREFIX}${i} present on Ludus, removing now"
            LUDUS_API_KEY=${ADMIN_API_KEY} ludus --url https://127.0.0.1:8081 user rm -i "${USER_PREFIX}${i}"
        fi
    done
else 
    echo "action must be 'run' or 'destroy'"
fi

echo "All tasks completed."
